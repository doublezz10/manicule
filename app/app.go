// Package app is the Wails service layer: every exported method is a
// frontend binding. It wires config, fleet, sources, library, downloads,
// OPDS, and the watcher into one lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/doublezz10/manicule/internal/config"
	"github.com/doublezz10/manicule/internal/device"
	"github.com/doublezz10/manicule/internal/download"
	"github.com/doublezz10/manicule/internal/fleet"
	"github.com/doublezz10/manicule/internal/library"
	"github.com/doublezz10/manicule/internal/opds"
	"github.com/doublezz10/manicule/internal/sources"
)

const Tier2Disclaimer = "This source connects to a third-party website not affiliated with this app, which bundles no content and no credentials. You are responsible for providing your own account and for ensuring your use complies with applicable law."

type SourceInfo struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Tier        int            `json:"tier"`
	Description string         `json:"description,omitempty"`
	Enabled     bool           `json:"enabled"`
	NeedsAuth   bool           `json:"needs_auth"`
	Status      sources.Status `json:"status"`
}

type SearchGroup struct {
	SourceID   string           `json:"source_id"`
	SourceName string           `json:"source_name"`
	State      string           `json:"state"` // ok | needs-auth | error | disabled
	Message    string           `json:"message,omitempty"`
	Results    []sources.Result `json:"results"`
}

type ServerStatus struct {
	Running     bool   `json:"running"`
	Port        int    `json:"port"`
	URL         string `json:"url"`     // localhost form
	LANURL      string `json:"lan_url"` // for QR + device
	PIN         string `json:"pin"`
	AuthEnabled bool   `json:"auth_enabled"`
	Username    string `json:"username"`
}

type Manicule struct {
	mu sync.RWMutex

	cfg           *config.Manager
	settings      *config.Settings
	fleet         *fleet.Fleet
	registry      *sources.Registry
	coverEnricher func(ctx context.Context, title string, authors []string) ([]byte, string, error)
	store         *library.Store
	downloads     *download.Manager
	opdsSrv       *opds.Server

	watchCancel context.CancelFunc
	ctx         context.Context
	tray        *application.SystemTray

	searchCancel context.CancelFunc      // in-flight SearchAll, superseded by newer queries
	searchID     string                  // owner of searchCancel; "" when idle
	searchCache  map[string]cachedSearch // query → results, short TTL

	deviceMu          sync.Mutex
	deviceClient      *device.Client
	deviceSt          *DeviceState
	deviceWatchCancel context.CancelFunc
	sendMu            sync.Mutex // one upload at a time; the firmware allows no more
}

func New() *Manicule {
	return &Manicule{}
}

func (m *Manicule) ServiceName() string { return "Manicule" }

func (m *Manicule) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	m.ctx = ctx
	cfg, err := config.NewManager()
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	m.cfg = cfg
	s, err := cfg.Load()
	if err != nil {
		return err
	}
	m.settings = s

	// Fleet: registry + overrides from settings; background refresh starts now.
	fl, err := fleet.New(filepath.Join(cfgDir(), "fleet-cache"), s.FleetOverrides)
	if err != nil {
		return err
	}
	m.fleet = fl
	fl.OnProbe(func(string) { m.emit("sources:status", m.SourceStatuses()) })
	fl.RunBackgroundRefresh()

	m.registry = sources.NewRegistry(sources.NewHTTPClient())
	m.searchCache = map[string]cachedSearch{}
	m.syncSources()

	// Wire OL cover enrichment for the ingest pipeline.
	if olSrc, ok := m.registry.Get("openlibrary"); ok {
		m.coverEnricher = func(ctx context.Context, title string, authors []string) ([]byte, string, error) {
			return sources.EnrichCover(ctx, sources.NewHTTPClient(), title, authors)
		}
		_ = olSrc // ensure the import is used
	}

	if s.LibraryPath != "" {
		if err := m.openLibrary(); err != nil {
			slog.Warn("library open failed at startup", "err", err)
		}
	}

	m.startDeviceWatcher()
	return nil
}

func (m *Manicule) ServiceShutdown() error {
	m.stopDeviceWatcher()
	m.stopWatch()
	if m.opdsSrv != nil {
		m.opdsSrv.Stop()
	}
	if m.downloads != nil {
		// in-flight tasks die with the process; queue is not persisted in v1
	}
	if m.store != nil {
		m.store.Close()
	}
	if m.fleet != nil {
		m.fleet.Stop()
	}
	return nil
}

func (m *Manicule) emit(name string, data any) {
	if a := application.Get(); a != nil {
		a.Event.Emit(name, data)
	}
}

func (m *Manicule) syncSources() {
	m.flushSearchCache() // enabled sources / credentials may have changed
	for _, src := range m.registry.All() {
		id := src.ID()
		enabled := m.settings.SourcesEnabled[id]
		m.registry.ApplyCredentials(id, credentialsFor(m.settings, id))
		if enabled {
			if base := m.fleet.BaseURL(id); base != "" {
				m.registry.ApplyBaseURL(id, base)
			}
		}
	}
}

func credentialsFor(s *config.Settings, id string) sources.Credentials {
	if c, ok := s.SourceCredentials[id]; ok {
		out := sources.Credentials{}
		for k, v := range c {
			out[k] = v
		}
		return out
	}
	return nil
}

// --- search ----------------------------------------------------------------

// metadataOnly sources enrich the app (covers, description backfill) but
// offer no downloads, so they never appear as search result providers.
var metadataOnly = map[string]bool{"openlibrary": true}

const searchCacheTTL = 2 * time.Minute

type cachedSearch struct {
	groups []SearchGroup
	saved  time.Time
}

// SearchAll fans out to every enabled source in parallel and groups results
// by source — no fake unified ranking (§2). Own-library hits come through
// ListLibrary from the same UI bar.
//
// Results stream to the frontend: a search:start event carries the per-source
// skeletons the moment the fan-out begins, and each source emits search:group
// as it finishes, so the UI paints the fastest catalog long before the slowest
// one returns. The blocking return value stays the final, complete answer
// (and serves repeat queries from a short-lived cache).
func (m *Manicule) SearchAll(query string) []SearchGroup {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}

	if groups, ok := m.cachedSearch(q); ok {
		return groups
	}

	// a newer query supersedes any in-flight run
	if m.searchCancel != nil {
		m.searchCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	searchID := m.claimSearch(cancel)

	groups := m.searchSkeleton()
	// emit a copy: workers start mutating `groups` right away, and the event
	// payload may be marshaled after those writes begin
	skeleton := make([]SearchGroup, len(groups))
	copy(skeleton, groups)
	m.emit("search:start", map[string]any{"query": q, "search_id": searchID, "groups": skeleton})

	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range groups {
		g := groups[i]
		if g.State != "searching" {
			continue
		}
		src, ok := m.registry.Get(g.SourceID)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(src sources.Source, idx int) {
			defer wg.Done()
			sctx, scancel := context.WithTimeout(ctx, 20*time.Second)
			defer scancel()
			start := time.Now()
			res, err := src.Search(sctx, q, 24)
			elapsed := time.Since(start)

			mu.Lock()
			defer mu.Unlock()
			g := groups[idx]
			if err != nil {
				slog.Warn("search source failed", "source", src.ID(), "elapsed", elapsed.String(), "err", err)
				if errors.Is(err, sources.ErrNeedsAuth) {
					g.State = "needs-auth"
				} else {
					g.State = "error"
					g.Message = friendlySearchErr(err)
				}
			} else {
				slog.Info("search source ok", "source", src.ID(), "elapsed", elapsed.String(), "results", len(res))
				g.State = "ok"
				g.Results = res
			}
			groups[idx] = g
			if ctx.Err() == nil {
				m.emit("search:group", map[string]any{"query": q, "search_id": searchID, "group": g})
			}
		}(src, i)
	}
	wg.Wait()

	m.releaseSearch(searchID)

	// cache only when something actually answered — retrying a failed search
	// must hit the network again, not the cache
	for _, g := range groups {
		if g.State == "ok" {
			m.cacheSearch(q, groups)
			break
		}
	}
	return groups
}

// friendlySearchErr turns transport noise into something a reader can act on;
// the raw error stays in the log.
func friendlySearchErr(err error) string {
	var nerr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &nerr) && nerr.Timeout() {
		return "timed out — check your connection"
	}
	msg := err.Error()
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "connection refused") {
		return "can't reach the catalog — check your connection"
	}
	if strings.Contains(msg, "EOF") || strings.Contains(msg, "reset by peer") {
		return "connection dropped — try again"
	}
	return msg
}

// searchSkeleton builds the initial per-source group list in stable order.
func (m *Manicule) searchSkeleton() []SearchGroup {
	groups := make([]SearchGroup, 0)
	for _, src := range m.registry.All() {
		if metadataOnly[src.ID()] {
			continue
		}
		enabled := m.settings.SourcesEnabled[src.ID()]
		g := SearchGroup{SourceID: src.ID(), SourceName: src.Name(), State: "searching"}
		if !enabled {
			g.State = "disabled"
		} else if src.NeedsAuth() {
			g.State = "needs-auth"
		}
		groups = append(groups, g)
	}
	return groups
}

func (m *Manicule) claimSearch(cancel context.CancelFunc) string {
	id := fmt.Sprintf("s%d", time.Now().UnixNano())
	m.mu.Lock()
	if m.searchCancel != nil {
		m.searchCancel() // belt-and-braces: nothing should be in flight here
	}
	m.searchCancel = cancel
	m.searchID = id
	m.mu.Unlock()
	return id
}

// releaseSearch clears the cancel hook only if this run still owns it (a
// superseding SearchAll may have claimed it already).
func (m *Manicule) releaseSearch(id string) {
	m.mu.Lock()
	if m.searchID == id {
		m.searchCancel = nil
		m.searchID = ""
	}
	m.mu.Unlock()
}

func (m *Manicule) cachedSearch(q string) ([]SearchGroup, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.searchCache[q]
	if !ok || time.Since(c.saved) > searchCacheTTL {
		return nil, false
	}
	return c.groups, true
}

func (m *Manicule) cacheSearch(q string, groups []SearchGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.searchCache) > 32 {
		m.searchCache = map[string]cachedSearch{}
	}
	m.searchCache[q] = cachedSearch{groups: groups, saved: time.Now()}
}

func (m *Manicule) flushSearchCache() {
	m.mu.Lock()
	m.searchCache = map[string]cachedSearch{}
	m.mu.Unlock()
}

func (m *Manicule) SourceStatuses() []map[string]any {
	hc := m.fleet.Snapshot()
	out := []map[string]any{}
	for _, def := range m.registry.All() {
		st := map[string]any{
			"id": def.ID(), "name": def.Name(), "tier": def.Tier(),
			"enabled": m.settings.SourcesEnabled[def.ID()],
		}
		if eps, ok := hc[def.ID()]; ok && len(eps) > 0 {
			best := ""
			for _, h := range eps {
				if h.Status == "ok" {
					best = "ok"
					break
				}
			}
			if best == "" {
				best = "down"
			}
			st["health"] = best
		} else {
			st["health"] = "unknown"
		}
		out = append(out, st)
	}
	return out
}

// --- downloads --------------------------------------------------------------

func (m *Manicule) Download(result sources.Result, formatName string) (*download.Task, error) {
	if m.downloads == nil {
		return nil, fmt.Errorf("no library configured")
	}
	if _, ok := m.registry.Get(result.SourceID); !ok {
		return nil, fmt.Errorf("unknown source %q", result.SourceID)
	}
	var format *sources.Format
	for i := range result.Formats {
		if strings.EqualFold(result.Formats[i].Name, formatName) {
			format = &result.Formats[i]
			break
		}
	}
	if format == nil {
		format = sources.PreferFormat(result.Formats)
	}
	if format == nil {
		return nil, fmt.Errorf("no downloadable format")
	}
	t := m.downloads.Enqueue(result, *format)
	return t, nil
}

func (m *Manicule) GetQueue() []download.Task {
	if m.downloads == nil {
		return nil
	}
	return m.downloads.Snapshot()
}

func (m *Manicule) CancelTask(id string) {
	if m.downloads != nil {
		m.downloads.Cancel(id)
	}
}

func (m *Manicule) ClearFinishedQueue() {
	if m.downloads != nil {
		m.downloads.ClearFinished()
	}
}

func cfgDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "manicule")
}

// openLibrary initializes the store + download manager for the configured path.
func (m *Manicule) openLibrary() error {
	store, err := library.Open(m.settings.LibraryPath)
	if err != nil {
		return err
	}
	m.store = store
	m.downloads = download.New(store, m.settings.CleanOnImport, m.settings.ImageMaxWidth, 2,
		func(event string, data any) { m.emit(event, data) },
		m.resolveSource,
		m.probeSource,
	)
	m.downloads.SetFilingMode(m.settings.FilingMode)
	m.downloads.SetCoverEnricher(m.coverEnricher)
	m.downloads.SetDoneHook(m.maybeAutoSend)
	m.startWatch()
	// Start the OPDS server if enabled.
	if m.settings.ServerEnabled {
		if err := m.restartServer(); err != nil {
			slog.Warn("opds server failed to start", "err", err)
		}
	}
	return nil
}

func (m *Manicule) resolveSource(id string) (sources.Source, bool) {
	return m.registry.Get(id)
}

func (m *Manicule) probeSource(ctx context.Context, sourceID string) {
	m.fleet.ProbeSource(ctx, sourceID)
}
