// Package app is the Wails service layer: every exported method is a
// frontend binding. It wires config, fleet, sources, library, downloads,
// OPDS, and the watcher into one lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/doublezz10/manicule/internal/config"
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

	cfg       *config.Manager
	settings  *config.Settings
	fleet     *fleet.Fleet
	registry  *sources.Registry
	coverEnricher func(ctx context.Context, title string, authors []string) ([]byte, string, error)
	store     *library.Store
	downloads *download.Manager
	opdsSrv   *opds.Server

	watchCancel context.CancelFunc
	ctx         context.Context
	tray        *application.SystemTray
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
	return nil
}

func (m *Manicule) ServiceShutdown() error {
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

// SearchAll fans out to every enabled source in parallel and groups results
// by source — no fake unified ranking (§2). Own-library hits come through
// ListLibrary from the same UI bar.
func (m *Manicule) SearchAll(query string) []SearchGroup {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	var wg sync.WaitGroup
	groups := make([]SearchGroup, 0)
	var mu sync.Mutex

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
		idx := len(groups)
		mu.Lock()
		groups = append(groups, g)
		mu.Unlock()

		if g.State != "searching" {
			continue
		}
		wg.Add(1)
		go func(src sources.Source, idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			start := time.Now()
			res, err := src.Search(ctx, q, 24)
			elapsed := time.Since(start)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				slog.Warn("search source failed", "source", src.ID(), "elapsed", elapsed.String(), "err", err)
				if errors.Is(err, sources.ErrNeedsAuth) {
					groups[idx].State = "needs-auth"
				} else {
					groups[idx].State = "error"
					groups[idx].Message = err.Error()
				}
				return
			}
			slog.Info("search source ok", "source", src.ID(), "elapsed", elapsed.String(), "results", len(res))
			groups[idx].State = "ok"
			groups[idx].Results = res
		}(src, idx)
	}
	wg.Wait()

	// stable order: tier then original order
	return groups
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
