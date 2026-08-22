// Package fleet implements the endpoint fleet subsystem: a community-maintained
// registry of source domains with a live → cache → embedded fetch chain, async
// health probing, and last-known-good failover. Search never blocks against
// last-known-good domains; failures re-probe in the background.
package fleet

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//go:embed snapshot.json
var embedded []byte

// Endpoint is one candidate base URL for a source.
type Endpoint struct {
	URL  string `json:"url"`
	Kind string `json:"kind"` // "api" | "opds" | "images"
}

type SourceDef struct {
	Name          string     `json:"name"`
	Tier          int        `json:"tier"`
	Description   string     `json:"description"`
	Endpoints     []Endpoint `json:"endpoints"`
	DownloadHosts []string   `json:"download_hosts,omitempty"`
	FeedPath      string     `json:"feed_path,omitempty"`
	AuthModel     string     `json:"auth_model,omitempty"`
	GeoBlockNotes string     `json:"geo_block_notes,omitempty"`
}

// Registry is the parsed sources registry.
type Registry struct {
	Version int                  `json:"version"`
	Updated string               `json:"updated"`
	Sources map[string]SourceDef `json:"sources"`
}

// Health tracks per-endpoint probe state.
type Health struct {
	Status    string    `json:"status"` // "unknown" | "ok" | "down"
	LatencyMS int64     `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

const (
	liveURL = "https://raw.githubusercontent.com/doublezz10/manicule/master/sources.json"
)

// Fleet manages the registry, its fetch chain, and background probing.
type Fleet struct {
	mu        sync.RWMutex
	reg       *Registry
	health    map[string]map[string]*Health // sourceID → endpoint URL → health
	cachePath string
	client    *http.Client
	override  map[string]string // sourceID → user-forced base URL

	onProbe func(sourceID string) // optional callback after each probe round
	stopCh  chan struct{}
	stopped sync.Once
}

func New(cacheDir string, overrides map[string]string) (*Fleet, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	f := &Fleet{
		health:    map[string]map[string]*Health{},
		cachePath: filepath.Join(cacheDir, "sources.json"),
		client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		override: overrides,
		stopCh:   make(chan struct{}),
	}
	if err := f.load(); err != nil {
		return nil, err
	}
	return f, nil
}

// load walks the fetch chain: disk cache → embedded snapshot (live fetch is
// kicked off in the background so startup never blocks on the network).
func (f *Fleet) load() error {
	if data, err := os.ReadFile(f.cachePath); err == nil {
		var reg Registry
		if json.Unmarshal(data, &reg) == nil && len(reg.Sources) > 0 {
			f.reg = &reg
			slog.Info("fleet: loaded registry from disk cache", "updated", reg.Updated)
			return nil
		}
	}
	var reg Registry
	if err := json.Unmarshal(embedded, &reg); err != nil {
		return fmt.Errorf("fleet: embedded snapshot corrupt: %w", err)
	}
	f.reg = &reg
	slog.Info("fleet: loaded embedded registry snapshot")
	return nil
}

// RefreshLive fetches the newest registry from raw.githubusercontent.com and,
// on success, writes it to the disk cache and swaps it in. Called async at
// launch and on a ~30-minute background cadence.
func (f *Fleet) RefreshLive(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liveURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "manicule/0.1 (+https://github.com/doublezz10/manicule)")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fleet: live registry fetch: HTTP %d", resp.StatusCode)
	}
	var reg Registry
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return fmt.Errorf("fleet: live registry decode: %w", err)
	}
	if len(reg.Sources) == 0 {
		return fmt.Errorf("fleet: live registry empty")
	}
	data, _ := json.MarshalIndent(&reg, "", "  ")
	if err := os.WriteFile(f.cachePath+".tmp", data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(f.cachePath+".tmp", f.cachePath); err != nil {
		return err
	}
	f.mu.Lock()
	f.reg = &reg
	f.mu.Unlock()
	slog.Info("fleet: refreshed live registry", "updated", reg.Updated)
	return nil
}

// BaseURL returns the current best base URL for a source:
// manual override first, then first healthy endpoint, then first listed.
func (f *Fleet) BaseURL(sourceID string) string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if u, ok := f.override[sourceID]; ok && u != "" {
		return u
	}
	def, ok := f.reg.Sources[sourceID]
	if !ok || len(def.Endpoints) == 0 {
		return ""
	}
	hs := f.health[sourceID]
	for _, ep := range def.Endpoints {
		if h, ok := hs[ep.URL]; ok && h.Status == "ok" {
			return ep.URL
		}
	}
	return def.Endpoints[0].URL
}

func (f *Fleet) Definition(sourceID string) (SourceDef, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	def, ok := f.reg.Sources[sourceID]
	return def, ok
}

func (f *Fleet) All() Registry {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return *f.reg
}

// SetOverride updates the manual personal-domain override for a source.
func (f *Fleet) SetOverride(sourceID, url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if url == "" {
		delete(f.override, sourceID)
	} else {
		f.override[sourceID] = url
	}
}

// ProbeAll checks every endpoint of every source concurrently and records
// health. It is deliberately polite: one GET per endpoint, short timeout.
func (f *Fleet) ProbeAll(ctx context.Context) {
	f.mu.RLock()
	type job struct{ source, url string }
	var jobs []job
	for id, def := range f.reg.Sources {
		for _, ep := range def.Endpoints {
			jobs = append(jobs, job{id, ep.URL})
		}
	}
	f.mu.RUnlock()

	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			h := probe(ctx, f.client, j.url)
			f.mu.Lock()
			if f.health[j.source] == nil {
				f.health[j.source] = map[string]*Health{}
			}
			f.health[j.source][j.url] = h
			f.mu.Unlock()
		}(j)
	}
	wg.Wait()
	if f.onProbe != nil {
		f.onProbe("")
	}
}

// ProbeSource re-probes one source's endpoints (used on download/search failure).
func (f *Fleet) ProbeSource(ctx context.Context, sourceID string) {
	def, ok := f.Definition(sourceID)
	if !ok {
		return
	}
	var wg sync.WaitGroup
	for _, ep := range def.Endpoints {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			h := probe(ctx, f.client, url)
			f.mu.Lock()
			if f.health[sourceID] == nil {
				f.health[sourceID] = map[string]*Health{}
			}
			f.health[sourceID][url] = h
			f.mu.Unlock()
		}(ep.URL)
	}
	wg.Wait()
	if f.onProbe != nil {
		f.onProbe(sourceID)
	}
}

// OnProbe registers a callback fired after probe rounds (for UI status pills).
func (f *Fleet) OnProbe(fn func(sourceID string)) { f.onProbe = fn }

func probe(ctx context.Context, client *http.Client, url string) *Health {
	start := time.Now()
	h := &Health{Status: "down"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		h.Error = err.Error()
		return h
	}
	req.Header.Set("User-Agent", "manicule/0.1 (+https://github.com/doublezz10/manicule)")
	resp, err := client.Do(req)
	if err != nil {
		h.Error = err.Error()
		h.LatencyMS = time.Since(start).Milliseconds()
		return h
	}
	resp.Body.Close()
	h.LatencyMS = time.Since(start).Milliseconds()
	h.CheckedAt = time.Now()
	// Anything answering HTTP is alive enough to try first; even 4xx means
	// DNS+TLS+routing work and may just need credentials.
	if resp.StatusCode < 500 {
		h.Status = "ok"
	} else {
		h.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return h
}

// Snapshot returns a copy of current health state for UI rendering.
func (f *Fleet) Snapshot() map[string]map[string]Health {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[string]map[string]Health, len(f.health))
	for s, eps := range f.health {
		m := make(map[string]Health, len(eps))
		for u, h := range eps {
			m[u] = *h
		}
		out[s] = m
	}
	return out
}

// RunBackgroundRefresh probes at launch and refreshes the live registry +
// re-probes roughly every 30 minutes until Stop is called.
func (f *Fleet) RunBackgroundRefresh() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		f.RefreshLive(ctx)
		cancel()
		f.ProbeAll(context.Background())
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-f.stopCh:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				err := f.RefreshLive(ctx)
				cancel()
				if err != nil {
					slog.Debug("fleet: background refresh skipped", "err", err)
				}
				pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Minute)
				f.ProbeAll(pctx)
				pcancel()
			}
		}
	}()
}

func (f *Fleet) Stop() { f.stopped.Do(func() { close(f.stopCh) }) }
