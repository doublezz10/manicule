package library

// Watch folder: copy-only ingest on a polling scan (simple, dependency-free,
// immune to network-drive event quirks). "Remove source file after successful
// import" is an explicit toggle, default OFF per spec §5.3.

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Watcher struct {
	interval time.Duration
	scanDir  string
	ingestor *Ingestor

	mu       sync.Mutex
	lastScan map[string]int64 // path → mtime, to avoid re-importing in-flight copies
}

func NewWatcher(dir string, interval time.Duration, ingestor *Ingestor) *Watcher {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &Watcher{scanDir: dir, interval: interval, ingestor: ingestor}
}

func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.scan()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.scan()
		}
	}
}

func (w *Watcher) scan() {
	if _, err := os.Stat(w.scanDir); err != nil {
		slog.Debug("watcher: scan failed", "dir", w.scanDir, "err", err)
		return
	}

	// Walk recursively: watched libraries are commonly structured trees
	// (e.g. Calibre's Author/Title/file.epub), so books rarely sit at the top.
	var candidates []string
	err := filepath.WalkDir(w.scanDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree — skip quietly, retry next tick
		}
		if path != w.scanDir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".epub", ".mobi", ".azw3":
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		slog.Debug("watcher: walk failed", "dir", w.scanDir, "err", err)
		return
	}

	for _, full := range candidates {
		st, err := os.Stat(full)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if !w.stableAndNew(full, st.ModTime().UnixNano()) {
			continue
		}
		slog.Info("watcher: importing", "file", filepath.Base(full))
		if _, err := w.ingestor.ImportFile(context.Background(), full, nil); err != nil {
			var dup *DuplicateError
			if asDup(err, &dup) {
				slog.Info("watcher: duplicate skipped", "file", filepath.Base(full))
			} else {
				slog.Warn("watcher: import failed", "file", filepath.Base(full), "err", err)
			}
		}
	}
}

// stableAndNew tracks mtimes so partially-copied files are skipped until the
// next scan confirms the size stopped changing.
func (w *Watcher) stableAndNew(path string, mtime int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lastScan == nil {
		w.lastScan = map[string]int64{}
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	key := path + ":" + itoa(st.Size())
	prev, seen := w.lastScan[key]
	w.lastScan[key] = mtime
	return seen && prev == mtime && st.Size() > 0
}

func itoa(n int64) string {
	return strconvFormat(n)
}

func strconvFormat(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func asDup(err error, target **DuplicateError) bool {
	dup, ok := err.(*DuplicateError)
	if ok {
		*target = dup
	}
	return ok
}
