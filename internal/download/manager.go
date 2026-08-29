// Package download manages the acquisition queue: parallel downloads from
// enabled sources, progress events to the UI, auto-retry across fleet
// candidates, and ingestion into the library on completion.
package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/doublezz10/manicule/internal/library"
	"github.com/doublezz10/manicule/internal/sources"
)

type State string

const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateDuplicate State = "duplicate"
)

type Task struct {
	ID         string         `json:"id"`
	SourceID   string         `json:"source_id"`
	ResultID   string         `json:"result_id,omitempty"` // source-specific book id, for UI correlation
	Title      string         `json:"title"`
	Authors    []string       `json:"authors"`
	CoverURL   string         `json:"cover_url,omitempty"`
	FormatName string         `json:"format_name"`
	State      State          `json:"state"`
	Error      string         `json:"error,omitempty"`
	BookID     int64          `json:"book_id,omitempty"`
	BytesDone  int64          `json:"bytes_done"`
	BytesTotal int64          `json:"bytes_total"` // 0 = unknown
	AddedAt    time.Time      `json:"added_at"`
	result     sources.Result `json:"-"`
	format     sources.Format
}

type Notifier func(event string, data any)

type Manager struct {
	mu     sync.Mutex
	tasks  []*Task
	sem    chan struct{}
	cancel map[string]context.CancelFunc

	// progress events are throttled so the UI gets ~4 updates/sec, not one
	// per network chunk
	lastProgressEmit time.Time

	store      *library.Store
	cleanOn    bool
	imageWidth int
	filingMode string
	notify     Notifier

	// coverEnricher fetches covers from OL when the EPUB has none.
	coverEnricher func(ctx context.Context, title string, authors []string) ([]byte, string, error)
	// resolveSource looks up an adapter by ID (wired by the app layer).
	resolveSource func(id string) (sources.Source, bool)
	// probeHook re-probes a source's fleet endpoints after a failure.
	probeHook func(ctx context.Context, sourceID string)
	// doneHook fires after a task lands in the library (auto-send to device).
	doneHook func(t *Task)
}

func New(store *library.Store, cleanOn bool, imageWidth int, concurrency int, notify Notifier,
	resolveSource func(id string) (sources.Source, bool), probeHook func(ctx context.Context, sourceID string)) *Manager {
	if concurrency < 1 {
		concurrency = 2
	}
	return &Manager{
		sem:           make(chan struct{}, concurrency),
		cancel:        map[string]context.CancelFunc{},
		store:         store,
		cleanOn:       cleanOn,
		imageWidth:    imageWidth,
		notify:        notify,
		resolveSource: resolveSource,
		probeHook:     probeHook,
	}
}

func (m *Manager) SetCleaning(on bool, imageWidth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanOn = on
	m.imageWidth = imageWidth
}

// SetCoverEnricher wires the OL cover enrichment hook into the ingest pipeline.
func (m *Manager) SetCoverEnricher(fn func(ctx context.Context, title string, authors []string) ([]byte, string, error)) {
	m.coverEnricher = fn
}

// SetDoneHook registers a callback fired once per successfully imported task.
func (m *Manager) SetDoneHook(fn func(t *Task)) {
	m.mu.Lock()
	m.doneHook = fn
	m.mu.Unlock()
}

// SetFilingMode updates the disk filing mode for new imports.
func (m *Manager) SetFilingMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filingMode = mode
}

// Enqueue adds a result+format to the queue and starts a worker.
func (m *Manager) Enqueue(r sources.Result, format sources.Format) *Task {
	m.mu.Lock()
	for _, t := range m.tasks { // one active task per source book
		if t.SourceID == r.SourceID && t.result.ID == r.ID &&
			(t.State == StateQueued || t.State == StateRunning) {
			m.mu.Unlock()
			return t
		}
	}
	t := &Task{
		ID:         newID(),
		SourceID:   r.SourceID,
		ResultID:   r.ID,
		Title:      r.Title,
		Authors:    r.Authors,
		CoverURL:   r.CoverURL,
		FormatName: format.Name,
		State:      StateQueued,
		AddedAt:    time.Now(),
		result:     r,
		format:     format,
	}
	m.tasks = append([]*Task{t}, m.tasks...) // newest first in the UI
	m.mu.Unlock()

	m.emit("queue:changed", m.Snapshot())
	go m.run(t)
	return t
}

func (m *Manager) run(t *Task) {
	m.sem <- struct{}{}
	defer func() { <-m.sem }()

	// a task failure must never take the app down
	defer func() {
		if r := recover(); r != nil {
			slog.Error("download task panicked", "task", t.ID, "title", t.Title, "panic", r)
			m.update(t, func() {
				t.State = StateFailed
				t.Error = fmt.Sprintf("internal error: %v", r)
			})
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancel[t.ID] = cancel
	m.mu.Unlock()
	defer cancel()

	m.update(t, func() { t.State = StateRunning })
	slog.Info("download started", "title", t.Title, "format", t.FormatName)

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("manicule-%s.%s",
		t.ID, extFor(t.FormatName)))
	defer os.Remove(tmp)

	err := m.downloadWithRetry(ctx, t, tmp)
	if err != nil {
		if ctx.Err() != nil {
			m.remove(t.ID)
			return
		}
		m.update(t, func() { t.State = StateFailed; t.Error = err.Error() })
		return
	}

	meta := &library.Book{
		Title:        t.Title,
		Authors:      t.Authors,
		Description:  t.result.Description,
		Language:     t.result.Language,
		Year:         library.ParseYear(t.result.Year),
		Subjects:     t.result.Subjects,
		SourceID:     t.result.SourceID,
		SourceBookID: t.result.ID,
	}
	ing := &library.Ingestor{
		Store:         m.store,
		CleanOnImport: m.cleanOn,
		ImageMaxWidth: m.imageWidth,
		FilingMode:    m.filingMode,
		CoverEnricher: m.coverEnricher,
	}
	book, err := ing.ImportFile(ctx, tmp, meta)
	if err != nil {
		var dup *library.DuplicateError
		if asDuplicate(err, &dup) {
			m.update(t, func() {
				t.State = StateDuplicate
				t.BookID = dup.ExistingID
				t.Error = "Already in library"
			})
			return
		}
		m.update(t, func() { t.State = StateFailed; t.Error = err.Error() })
		return
	}
	m.update(t, func() {
		t.State = StateDone
		t.BookID = book.Book.ID
	})
	m.emit("library:changed", nil)

	m.mu.Lock()
	hook := m.doneHook
	m.mu.Unlock()
	if hook != nil {
		hook(t)
	}
}

// setProgress records byte counts on a task; it returns true when the caller
// should emit a queue event (throttled so chunk-level callbacks don't flood
// the UI bridge).
func (m *Manager) setProgress(t *Task, done, total int64) bool {
	m.mu.Lock()
	t.BytesDone = done
	t.BytesTotal = total
	emit := time.Since(m.lastProgressEmit) >= 250*time.Millisecond
	if emit {
		m.lastProgressEmit = time.Now()
	}
	m.mu.Unlock()
	return emit
}

// downloadWithRetry streams the file; on transport failure it re-probes the
// source's fleet endpoints so the next candidate URL is chosen (failover at
// link-selection, never mid-download).
func (m *Manager) downloadWithRetry(ctx context.Context, t *Task, dest string) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			if prober := m.probeHook; prober != nil {
				prober(ctx, t.SourceID)
			}
		}
		f, err := os.Create(dest)
		if err != nil {
			return err
		}
		src, ok := m.resolveSource(t.SourceID)
		if !ok {
			err = fmt.Errorf("source %q unavailable", t.SourceID)
		} else {
			err = src.Download(ctx, t.format, f, func(done, total int64) {
				if m.setProgress(t, done, total) {
					m.emit("queue:changed", m.Snapshot())
				}
			})
		}
		f.Close()
		if err == nil {
			st, serr := os.Stat(dest)
			if serr == nil && st.Size() < 1024 {
				lastErr = fmt.Errorf("suspiciously small response (%d bytes)", st.Size())
				continue // likely an HTML error page — retry next candidate
			}
			return nil
		}
		os.Remove(dest)
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func (m *Manager) Cancel(taskID string) {
	m.mu.Lock()
	cancel, ok := m.cancel[taskID]
	m.mu.Unlock()
	if ok {
		cancel()
	}
}

func (m *Manager) ClearFinished() {
	m.mu.Lock()
	kept := m.tasks[:0]
	for _, t := range m.tasks {
		if t.State != StateDone && t.State != StateFailed && t.State != StateDuplicate {
			kept = append(kept, t)
		}
	}
	m.tasks = kept
	m.mu.Unlock()
	m.emit("queue:changed", m.Snapshot())
}

func (m *Manager) remove(taskID string) {
	m.mu.Lock()
	kept := m.tasks[:0]
	for _, t := range m.tasks {
		if t.ID != taskID {
			kept = append(kept, t)
		}
	}
	m.tasks = kept
	m.mu.Unlock()
	m.emit("queue:changed", m.Snapshot())
}

func (m *Manager) update(t *Task, fn func()) {
	m.mu.Lock()
	fn()
	m.mu.Unlock()
	m.emit("queue:changed", m.Snapshot())
}

func (m *Manager) Snapshot() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, len(m.tasks))
	for i, t := range m.tasks {
		cp := *t
		out[i] = cp
	}
	return out
}

func (m *Manager) emit(name string, data any) {
	if m.notify != nil {
		m.notify(name, data)
	}
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func extFor(format string) string {
	switch format {
	case "EPUB":
		return "epub"
	case "MOBI":
		return "mobi"
	default:
		return "bin"
	}
}

func asDuplicate(err error, target **library.DuplicateError) bool {
	dup, ok := err.(*library.DuplicateError)
	if ok {
		*target = dup
	}
	return ok
}
