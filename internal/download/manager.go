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
	Title      string         `json:"title"`
	Authors    []string       `json:"authors"`
	CoverURL   string         `json:"cover_url,omitempty"`
	FormatName string         `json:"format_name"`
	State      State          `json:"state"`
	Error      string         `json:"error,omitempty"`
	BookID     int64          `json:"book_id,omitempty"`
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

	store      *library.Store
	cleanOn    bool
	imageWidth int
	notify     Notifier

	// resolveSource looks up an adapter by ID (wired by the app layer).
	resolveSource func(id string) (sources.Source, bool)
	// probeHook re-probes a source's fleet endpoints after a failure.
	probeHook func(ctx context.Context, sourceID string)
}

func New(store *library.Store, cleanOn bool, imageWidth int, concurrency int, notify Notifier,
	resolveSource func(id string) (sources.Source, bool), probeHook func(ctx context.Context, sourceID string)) *Manager {
	if concurrency < 1 {
		concurrency = 2
	}
	return &Manager{
		sem:        make(chan struct{}, concurrency),
		cancel:     map[string]context.CancelFunc{},
		store:      store,
		cleanOn:    cleanOn,
		imageWidth: imageWidth,
		notify:     notify,
	}
}

func (m *Manager) SetCleaning(on bool, imageWidth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanOn = on
	m.imageWidth = imageWidth
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
		SourceID:     t.result.SourceID,
		SourceBookID: t.result.ID,
	}
	ing := &library.Ingestor{
		Store:         m.store,
		CleanOnImport: m.cleanOn,
		ImageMaxWidth: m.imageWidth,
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
			err = src.Download(ctx, t.format, f)
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
