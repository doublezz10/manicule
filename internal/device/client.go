// CrossPoint HTTP client: status, file listing, deletes, and OPDS
// provisioning. All endpoints are plain http://:80 with no auth — this is
// home-LAN territory while the device is in transfer mode.
package device

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client addresses one reader. Base is http://<ip>/ (firmware serves :80);
// WSPort carries the WebSocket upload channel.
type Client struct {
	base   string
	wsPort int
	wsURL  string // test hook: full WS endpoint override
	http   *http.Client
}

func New(ip string, wsPort int) *Client {
	return &Client{
		base:   "http://" + ip,
		wsPort: wsPort,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "tcp4", addr) // IPv4-only, house policy
				},
			},
		},
	}
}

func (c *Client) Base() string { return c.base }

// Status mirrors GET /api/status.
type Status struct {
	Version  string `json:"version"`
	IP       string `json:"ip"`
	Mode     string `json:"mode"` // "STA" (joined wifi) or "AP" (hotspot)
	RSSI     int    `json:"rssi"`
	FreeHeap int64  `json:"freeHeap"`
	Uptime   int64  `json:"uptime"`
	Device   string `json:"device"` // "X3" | "X4"
}

// Status identifies the reader. It doubles as a reachability check.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var st Status
	if err := c.getJSON(ctx, "/api/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Entry is one row of GET /api/files.
type Entry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	IsDir  bool   `json:"isDirectory"`
	IsEpub bool   `json:"isEpub"`
}

// ListDir lists one directory on the SD card. The firmware hides dotfiles
// and protects "System Volume Information" and "XTCache" on its own.
func (c *Client) ListDir(ctx context.Context, path string) ([]Entry, error) {
	var out []Entry
	if err := c.getJSON(ctx, "/api/files?path="+url.QueryEscape(path), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes files (and empty folders) by absolute device path.
func (c *Client) Delete(ctx context.Context, paths []string) error {
	payload, _ := json.Marshal(paths)
	form := url.Values{"paths": {string(payload)}}
	return c.postForm(ctx, "/delete", form)
}

// Mkdir creates a folder under parent.
func (c *Client) Mkdir(ctx context.Context, parent, name string) error {
	form := url.Values{"name": {name}, "path": {parent}}
	return c.postForm(ctx, "/mkdir", form)
}

// Download streams a device file (GET /download) into w.
func (c *Client) Download(ctx context.Context, path string, w io.Writer) error {
	resp, err := c.get(ctx, "/download?path="+url.QueryEscape(path))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("device: HTTP %d downloading %s", resp.StatusCode, path)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// OPDSServer is one saved catalog on the device.
type OPDSServer struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username,omitempty"`
	HasPassword bool   `json:"hasPassword"`
}

// ListOPDS reads the device's saved OPDS servers (passwords never returned).
func (c *Client) ListOPDS(ctx context.Context) ([]OPDSServer, error) {
	var out []OPDSServer
	if err := c.getJSON(ctx, "/api/opds", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetOPDS adds or updates a saved OPDS server. An empty password preserves
// the stored one on updates.
func (c *Client) SetOPDS(ctx context.Context, srv OPDSServer, password string) error {
	body := map[string]string{"name": srv.Name, "url": srv.URL, "username": srv.Username}
	if srv.Index > 0 {
		body["index"] = fmt.Sprintf("%d", srv.Index)
	}
	if password != "" {
		body["password"] = password
	}
	return c.postJSON(ctx, "/api/opds", body)
}

// --- internals --------------------------------------------------------------

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device %s: %w", c.base, err)
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	resp, err := c.get(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("device: HTTP %d for %s", resp.StatusCode, path)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("device %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("device: HTTP %d for %s", resp.StatusCode, path)
	}
	return nil
}

func (c *Client) postJSON(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("device %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("device: HTTP %d for %s", resp.StatusCode, path)
	}
	return nil
}

// UploadFallback pushes a file via multipart POST /upload — the documented
// HTTP path without progress. Used when the WebSocket channel is unavailable
// (older firmware, WS port blocked).
func (c *Client) UploadFallback(ctx context.Context, dir, filename string, r io.Reader, size int64) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filename)
		if err == nil {
			_, err = io.CopyN(part, r, size)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/upload?path="+url.QueryEscape(dir), pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.ContentLength = -1
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("device %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("device: HTTP %d uploading %s", resp.StatusCode, filename)
	}
	return nil
}
