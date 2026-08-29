package download

import (
	"testing"
	"time"

	"github.com/doublezz10/manicule/internal/sources"
)

func TestSetProgressThrottle(t *testing.T) {
	m := New(nil, false, 0, 1, nil, nil, nil)
	tk := &Task{}

	if !m.setProgress(tk, 1, 100) {
		t.Fatal("first progress tick should emit")
	}
	for i := 0; i < 50; i++ {
		if m.setProgress(tk, int64(i+2), 100) {
			t.Fatalf("tick %d within throttle window should not emit", i)
		}
	}
	time.Sleep(260 * time.Millisecond)
	if !m.setProgress(tk, 99, 100) {
		t.Fatal("tick after the throttle window should emit")
	}
	if tk.BytesDone != 99 || tk.BytesTotal != 100 {
		t.Fatalf("counters not updated: done=%d total=%d", tk.BytesDone, tk.BytesTotal)
	}
}

// Regression: New must wire the resolve hook — a nil one panics the whole
// app on the first real download (found live, 2026-08-29).
func TestNewWiresCallbacks(t *testing.T) {
	called := false
	m := New(nil, false, 0, 1, nil,
		func(id string) (sources.Source, bool) { called = true; return nil, false }, nil)
	if m.resolveSource == nil {
		t.Fatal("resolveSource not wired — Enqueue would panic on first download")
	}
	m.resolveSource("x")
	if !called {
		t.Fatal("resolveSource field points elsewhere")
	}
}
