// CrossPoint reader over Wi-Fi: background discovery, library sync planning,
// one-click send, and OPDS provisioning straight onto the device. Replaces
// the SD-card drop with a network call when the reader is in transfer mode.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/doublezz10/manicule/internal/device"
	"github.com/doublezz10/manicule/internal/download"
	"github.com/doublezz10/manicule/internal/library"
	"github.com/doublezz10/manicule/internal/opds"
)

// deviceScanInterval is how often the watcher asks the LAN for a reader;
// deviceWait is how long each discovery round listens for replies.
const (
	deviceScanInterval = 8 * time.Second
	deviceWait         = 1500 * time.Millisecond
)

// DeviceState is the Devices page model: connection phase plus the sync plan.
type DeviceState struct {
	Phase     string              `json:"phase"` // "searching" | "connected" | "offline"
	Status    *device.Status      `json:"status,omitempty"`
	OnDevice  []device.Match      `json:"on_device"`
	Missing   []device.Match      `json:"missing"`
	Orphan    []device.DeviceFile `json:"orphan"`
	LastError string              `json:"last_error,omitempty"`
}

// startDeviceWatcher polls for a reader and emits connect/disconnect events.
func (m *Manicule) startDeviceWatcher() {
	m.stopDeviceWatcher()
	ctx, cancel := context.WithCancel(m.ctx)
	m.deviceWatchCancel = cancel
	go m.deviceWatchLoop(ctx)
}

func (m *Manicule) stopDeviceWatcher() {
	if m.deviceWatchCancel != nil {
		m.deviceWatchCancel()
		m.deviceWatchCancel = nil
	}
}

func (m *Manicule) deviceWatchLoop(ctx context.Context) {
	seen := false
	for {
		found := m.tryConnect()
		if found != seen {
			seen = found
			st := m.DeviceStateSnapshot()
			if found {
				slog.Info("device connected", "model", st.Status.Device, "version", st.Status.Version, "ip", st.Status.IP)
				m.emit("device:connected", st)
			} else {
				m.emit("device:disconnected", st)
			}
			m.emit("device:changed", st)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(deviceScanInterval):
		}
	}
}

// tryConnect attempts discovery + status probe; on success it caches the
// client and flips the state to connected.
func (m *Manicule) tryConnect() bool {
	found, err := device.Discover(deviceWait)
	if err != nil || len(found) == 0 {
		m.dropDevice("")
		return false
	}
	c := device.New(found[0].IP, found[0].WSPort)
	st, err := c.Status(context.Background())
	if err != nil {
		m.dropDevice(err.Error())
		return false
	}
	m.deviceMu.Lock()
	m.deviceClient = c
	m.deviceSt = &DeviceState{Phase: "connected", Status: st}
	m.deviceMu.Unlock()
	return true
}

// dropDevice clears a stale client so the next scan re-discovers from scratch.
func (m *Manicule) dropDevice(lastErr string) {
	m.deviceMu.Lock()
	m.deviceClient = nil
	if m.deviceSt == nil || m.deviceSt.Phase != "connected" {
		m.deviceSt = &DeviceState{Phase: "offline", LastError: lastErr}
	} else {
		m.deviceSt = &DeviceState{Phase: "offline", Status: m.deviceSt.Status, LastError: lastErr}
	}
	m.deviceMu.Unlock()
}

// DeviceStateSnapshot returns the last known state without touching the net.
func (m *Manicule) DeviceStateSnapshot() *DeviceState {
	m.deviceMu.Lock()
	defer m.deviceMu.Unlock()
	if m.deviceSt == nil {
		return &DeviceState{Phase: "searching"}
	}
	return m.deviceSt
}

// --- bindings ----------------------------------------------------------------

// DeviceScan refreshes the connection and rebuilds the sync plan from the
// library. Safe to call any time; cheap when the watcher already knows the
// device is gone.
func (m *Manicule) DeviceScan() *DeviceState {
	if m.store == nil {
		return &DeviceState{Phase: "offline", LastError: "no library configured"}
	}
	if m.connected() == nil {
		m.tryConnect()
	}
	state := m.buildDeviceState()
	m.deviceMu.Lock()
	m.deviceSt = state
	m.deviceMu.Unlock()
	m.emit("device:changed", state)
	return state
}

// SendToDevice pushes one library book to the reader.
func (m *Manicule) SendToDevice(bookID int64) (*DeviceState, error) {
	if err := m.sendBooks([]int64{bookID}); err != nil {
		return m.DeviceStateSnapshot(), err
	}
	return m.DeviceScan(), nil
}

// SyncDevice sends every library book the reader is missing.
func (m *Manicule) SyncDevice() (*DeviceState, error) {
	state := m.DeviceScan()
	if state.Phase != "connected" {
		return state, fmt.Errorf("no reader connected — put the device in File Transfer mode")
	}
	var ids []int64
	for _, mi := range state.Missing {
		ids = append(ids, mi.BookID)
	}
	if len(ids) == 0 {
		return state, nil
	}
	if err := m.sendBooks(ids); err != nil {
		return m.DeviceStateSnapshot(), err
	}
	return m.DeviceScan(), nil
}

// RemoveFromDevice deletes files from the reader's SD card.
func (m *Manicule) RemoveFromDevice(paths []string) (*DeviceState, error) {
	c := m.connected()
	if c == nil {
		return m.DeviceStateSnapshot(), fmt.Errorf("no reader connected")
	}
	if err := c.Delete(context.Background(), paths); err != nil {
		return m.DeviceStateSnapshot(), err
	}
	return m.DeviceScan(), nil
}

// ProvisionDeviceOPDS writes manicule's catalog into the reader's saved OPDS
// server list over Wi-Fi — the SD-card drop, minus the SD card.
func (m *Manicule) ProvisionDeviceOPDS() error {
	c := m.connected()
	if c == nil {
		return fmt.Errorf("no reader connected — put the device in File Transfer mode")
	}
	status := m.ServerStatus()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// upsert by name so a re-provision updates the stored PIN in place
	servers, _ := c.ListOPDS(ctx)
	entry := device.OPDSServer{Name: "manicule", URL: status.LANURL, Username: opds.Username}
	for _, s := range servers {
		if s.Name == "manicule" {
			entry.Index = s.Index
		}
	}
	return c.SetOPDS(ctx, entry, status.PIN)
}

// --- send pipeline -------------------------------------------------------------

func (m *Manicule) connected() *device.Client {
	m.deviceMu.Lock()
	defer m.deviceMu.Unlock()
	return m.deviceClient
}

// buildDeviceState probes the device and plans the sync against the library.
func (m *Manicule) buildDeviceState() *DeviceState {
	c := m.connected()
	if c == nil {
		return &DeviceState{Phase: "offline"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := c.Status(ctx)
	if err != nil {
		m.deviceMu.Lock()
		m.deviceClient = nil
		m.deviceMu.Unlock()
		return &DeviceState{Phase: "offline", LastError: err.Error()}
	}
	files, err := device.WalkBooks(ctx, c)
	if err != nil {
		return &DeviceState{Phase: "connected", Status: st, LastError: "scan failed: " + err.Error()}
	}
	plan := device.PlanBooks(m.deviceLibBooks(), files)
	return &DeviceState{Phase: "connected", Status: st, OnDevice: plan.OnDevice, Missing: plan.Missing, Orphan: plan.Orphan}
}

// deviceLibBooks maps the store onto the planner's input, choosing the file
// to push: cleaned EPUB first, then original EPUB, then any format.
func (m *Manicule) deviceLibBooks() []device.LibBook {
	books, err := m.store.List("", "added", 0, 1_000_000)
	if err != nil {
		return nil
	}
	out := make([]device.LibBook, 0, len(books))
	for _, bw := range books {
		pick := pickSendFile(&bw)
		if pick == nil {
			continue
		}
		out = append(out, device.LibBook{
			ID:       bw.Book.ID,
			Title:    bw.Book.Title,
			Author:   bw.Book.FirstAuthor(),
			Format:   pick.Format,
			SendPath: m.store.AbsPath(pick.Path),
			SendSize: pick.SizeBytes,
		})
	}
	return out
}

// pickSendFile prefers the cleaned derivative (it's optimized for this
// e-ink panel), then the original EPUB, then whatever exists.
func pickSendFile(bw *library.BookWithFiles) *library.BookFile {
	var fallback *library.BookFile
	for i := range bw.Files {
		f := &bw.Files[i]
		if fallback == nil {
			fallback = f
		}
		if !f.IsOriginal && strings.EqualFold(f.Format, "EPUB") {
			return f
		}
	}
	for i := range bw.Files {
		f := &bw.Files[i]
		if f.IsOriginal && strings.EqualFold(f.Format, "EPUB") {
			return f
		}
	}
	return fallback
}

// sendBooks pushes the given books one at a time (the firmware allows a
// single upload), emitting device:progress events as each lands.
func (m *Manicule) sendBooks(ids []int64) error {
	c := m.connected()
	if c == nil {
		return fmt.Errorf("no reader connected — put the device in File Transfer mode")
	}
	byID := map[int64]device.LibBook{}
	for _, b := range m.deviceLibBooks() {
		byID[b.ID] = b
	}

	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	for _, id := range ids {
		b, ok := byID[id]
		if !ok {
			return fmt.Errorf("book %d has no sendable file", id)
		}
		if err := m.sendOne(c, b); err != nil {
			slog.Warn("device send failed", "book", b.Title, "err", err)
			return err
		}
	}
	return nil
}

func (m *Manicule) sendOne(c *device.Client, b device.LibBook) error {
	f, err := os.Open(b.SendPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", b.SendPath, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	remote := device.RemotePathFor(b.Author, b.Title, b.Format)
	device.EnsureDirs(context.Background(), c, remote)

	dir := remote[:strings.LastIndexByte(remote, '/')]
	name := remote[strings.LastIndexByte(remote, '/')+1:]

	lastEmit := time.Now()
	total := st.Size()
	err = c.Upload(context.Background(), dir, name, f, total, func(done, _ int64) {
		if time.Since(lastEmit) < 250*time.Millisecond && done < total {
			return
		}
		lastEmit = time.Now()
		m.emit("device:progress", map[string]any{
			"book_id": b.ID, "title": b.Title,
			"done": done, "total": total,
		})
	})
	if err != nil {
		m.emit("device:progress", map[string]any{
			"book_id": b.ID, "title": b.Title, "error": err.Error(),
		})
		return err
	}
	m.emit("device:progress", map[string]any{
		"book_id": b.ID, "title": b.Title, "done": total, "total": total,
	})
	return nil
}

// maybeAutoSend is the download-manager completion hook: with the setting on
// and a reader present, new arrivals hop straight onto the device.
func (m *Manicule) maybeAutoSend(t *download.Task) {
	if !m.settings.AutoSendDevice || t.BookID <= 0 {
		return
	}
	if m.connected() == nil {
		return
	}
	go func() {
		if err := m.sendBooks([]int64{t.BookID}); err != nil {
			return // logged in sendBooks; silent here — auto-send is opportunistic
		}
		m.emit("device:auto-sent", map[string]any{"book_id": t.BookID, "title": t.Title})
	}()
}
