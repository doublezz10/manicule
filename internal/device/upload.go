// WebSocket upload (firmware port 81) — the fast path the firmware's own
// file manager and Calibre plugin use. Protocol: client sends
// "START:<filename>:<size>:<path>", server answers "READY", client streams
// binary, server reports "PROGRESS:<received>:<total>" every 64 KB and ends
// with "DONE" (or "ERROR:<message>"). Only one upload may run at a time;
// incomplete uploads are deleted by the device on disconnect.
package device

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// 8KB frames: the reader is an ESP32 with ~57KB free heap — bigger
	// frames overflow its socket buffer and it drops the connection
	uploadChunkSize = 8 * 1024
	uploadStall     = 30 * time.Second // watchdog: abort when the device goes quiet
)

// Upload streams r (size bytes) onto the device's SD card at dir/filename,
// reporting progress as bytes confirmed by the device. It falls back to the
// multipart HTTP path when the WebSocket channel cannot be established.
func (c *Client) Upload(ctx context.Context, dir, filename string, r io.Reader, size int64, onProgress func(done, total int64)) error {
	if err := c.uploadWS(ctx, dir, filename, r, size, onProgress); err != nil {
		if errors.Is(err, errWSDial) {
			// older firmware or blocked WS port — the documented HTTP
			// endpoint still lands the file, just without per-chunk progress
			return c.UploadFallback(ctx, dir, filename, r, size)
		}
		return err
	}
	return nil
}

var errWSDial = errors.New("websocket unavailable")

// frame is one device→client message: control text or a PROGRESS count.
type frame struct {
	text  string
	total int64
}

func (c *Client) uploadWS(ctx context.Context, dir, filename string, r io.Reader, size int64, onProgress func(done, total int64)) error {
	wsCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(wsCtx, 10*time.Second)
	conn, _, err := websocket.Dial(dialCtx, c.wsEndpoint(), nil)
	dialCancel()
	if err != nil {
		return fmt.Errorf("%w: %v", errWSDial, err)
	}
	defer conn.CloseNow()

	start := fmt.Sprintf("START:%s:%d:%s", filename, size, dir)
	if err := conn.Write(wsCtx, websocket.MessageText, []byte(start)); err != nil {
		return fmt.Errorf("device upload: %w", err)
	}

	// reader goroutine: drains the device's PROGRESS/DONE/ERROR frames so the
	// socket never backs up while we stream
	frames := make(chan frame, 32)
	readErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.Read(wsCtx)
			if err != nil {
				readErr <- err
				close(frames)
				return
			}
			text := string(data)
			if strings.HasPrefix(text, "PROGRESS:") {
				parts := strings.SplitN(text[len("PROGRESS:"):], ":", 2)
				var total int64
				if len(parts) == 2 {
					fmt.Sscanf(parts[1], "%d", &total)
				}
				frames <- frame{text: "PROGRESS", total: total}
				continue
			}
			frames <- frame{text: text}
			if text == "DONE" || strings.HasPrefix(text, "ERROR") {
				close(frames)
				return
			}
		}
	}()

	msg, err := expect(frames, readErr, "READY")
	if err != nil {
		return err
	}
	_ = msg

	var (
		mu       sync.Mutex
		sent     int64
		lastSent int64
	)
	report := func(done int64) {
		if onProgress != nil {
			onProgress(done, size)
		}
	}

	buf := make([]byte, uploadChunkSize)
	for sent < size {
		n, rerr := r.Read(buf)
		if n > 0 {
			if werr := conn.Write(wsCtx, websocket.MessageBinary, buf[:n]); werr != nil {
				return fmt.Errorf("device upload: %w", werr)
			}
			mu.Lock()
			sent += int64(n)
			now := sent
			mu.Unlock()
			report(now)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("device upload: read source: %w", rerr)
		}
		// fold in device-confirmed progress without blocking the writer
		select {
		case f, ok := <-frames:
			if !ok {
				return drainError(readErr)
			}
			if f.text == "ERROR" || strings.HasPrefix(f.text, "ERROR") {
				return fmt.Errorf("device upload: %s", f.text)
			}
			if f.total > lastSent {
				lastSent = f.total
			}
		default:
		}
	}

	// all bytes written — the device confirms with a final PROGRESS + DONE
	for {
		f, ok := <-frames
		if !ok {
			return drainError(readErr)
		}
		switch {
		case f.text == "DONE":
			if lastSent < size {
				report(size)
			}
			return nil
		case strings.HasPrefix(f.text, "ERROR"):
			return fmt.Errorf("device upload: %s", f.text)
		case f.text == "PROGRESS":
			if f.total > lastSent {
				lastSent = f.total
			}
		}
	}
}

func expect(frames <-chan frame, readErr <-chan error, want string) (frame, error) {
	select {
	case f, ok := <-frames:
		if !ok {
			return frame{}, drainError(readErr)
		}
		if f.text != want {
			return frame{}, fmt.Errorf("device upload: expected %s, got %s", want, f.text)
		}
		return f, nil
	case err := <-readErr:
		return frame{}, fmt.Errorf("device upload: %w", err)
	}
}

func drainError(readErr <-chan error) error {
	select {
	case err := <-readErr:
		if strings.Contains(err.Error(), "normal closure") || strings.Contains(err.Error(), "EOF") {
			return fmt.Errorf("device upload: connection closed before DONE")
		}
		return fmt.Errorf("device upload: %w", err)
	default:
		return fmt.Errorf("device upload: connection closed before DONE")
	}
}

func (c *Client) host() string {
	h := strings.TrimPrefix(c.base, "http://")
	return strings.TrimSuffix(h, "/")
}

func (c *Client) wsEndpoint() string {
	if c.wsURL != "" {
		return c.wsURL
	}
	return fmt.Sprintf("ws://%s:%d/", c.host(), c.wsPort)
}
