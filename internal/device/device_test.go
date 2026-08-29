package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// fakeDevice implements the documented firmware surface: /api/status,
// /api/files, /delete, /mkdir, /api/opds, /upload (multipart) and the
// WebSocket upload channel on its own listener.
type fakeDevice struct {
	mu    sync.Mutex
	files map[string]int64 // absolute path → size
	srv   *httptest.Server
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{files: map[string]int64{}}
}

func (f *fakeDevice) put(path string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = size
}

func (f *fakeDevice) size(path string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.files[path]
	return n, ok
}

func (f *fakeDevice) remove(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, path)
}

func (f *fakeDevice) snapshot() map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(f.files))
	for k, v := range f.files {
		out[k] = v
	}
	return out
}

func (f *fakeDevice) start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"1.0.0","ip":"192.168.1.50","mode":"STA","rssi":-50,"freeHeap":100000,"uptime":42,"device":"X3"}`)
	})
	mux.HandleFunc("/api/files", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		w.Header().Set("Content-Type", "application/json")
		seen := map[string]bool{}
		out := []string{}
		for p, sz := range f.snapshot() {
			rel := strings.TrimPrefix(p, "/")
			if path != "/" && !strings.HasPrefix(rel, strings.Trim(path, "/")+"/") {
				continue
			}
			rest := strings.TrimPrefix(rel, strings.TrimPrefix(path, "/"))
			rest = strings.TrimPrefix(rest, "/")
			if i := strings.IndexByte(rest, '/'); i >= 0 { // child directory
				name := rest[:i]
				if !seen[name] {
					seen[name] = true
					out = append(out, fmt.Sprintf(`{"name":%q,"size":0,"isDirectory":true,"isEpub":false}`, name))
				}
				continue
			}
			out = append(out, fmt.Sprintf(`{"name":%q,"size":%d,"isDirectory":false,"isEpub":true}`, rest, sz))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(out, ","))
	})
	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		var paths []string
		if raw := r.FormValue("paths"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &paths)
		}
		for _, p := range paths {
			f.remove(p)
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/mkdir", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/opds", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			fmt.Fprint(w, `[{"index":0,"name":"old","url":"http://x/opds","username":"u","hasPassword":true}]`)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil || len(r.MultipartForm.File["file"]) == 0 {
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		dst := r.URL.Query().Get("path") + "/" + r.MultipartForm.File["file"][0].Filename
		file, _ := r.MultipartForm.File["file"][0].Open()
		data, _ := io.ReadAll(file)
		f.put(dst, int64(len(data)))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) { f.serveWS(w, r) })
	f.srv = httptest.NewServer(mux)
}

// serveWS runs the documented upload protocol against a test client.
func (f *fakeDevice) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := r.Context()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return
	}
	var filename, dir string
	var size int64
	if _, err := fmt.Sscanf(string(data), "START:%s", new(string)); err != nil {
		conn.Write(ctx, websocket.MessageText, []byte("ERROR:Invalid START format"))
		return
	}
	parts := strings.SplitN(string(data), ":", 4)
	if len(parts) == 4 {
		filename, size, dir = parts[1], parseInt(parts[2]), parts[3]
	}
	conn.Write(ctx, websocket.MessageText, []byte("READY"))
	var got int64
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageText {
			continue
		}
		got += int64(len(data))
		conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("PROGRESS:%d:%d", got, size)))
		if got >= size {
			conn.Write(ctx, websocket.MessageText, []byte("DONE"))
			f.put(dir+"/"+filename, got)
			return
		}
	}
}

func parseInt(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

func TestParseReply(t *testing.T) {
	host, port, ok := parseReply("crosspoint (on myx3);81")
	if !ok || host != "myx3" || port != 81 {
		t.Fatalf("parseReply = %q %d %v", host, port, ok)
	}
	if _, _, ok := parseReply("some other gadget;81"); ok {
		t.Fatal("non-crosspoint reply accepted")
	}
	if _, _, ok := parseReply("crosspoint (on x);notaport"); ok {
		t.Fatal("bad port accepted")
	}
}

func TestSanitizeNameAndRemotePath(t *testing.T) {
	if got := SanitizeName(`A*Very:Long/Title?`); got != "A Very Long Title" {
		t.Fatalf("SanitizeName = %q", got)
	}
	if got := RemotePathFor("Jules Verne", "Twenty Thousand Leagues Under the Sea", "EPUB"); got != "/Jules Verne/Twenty Thousand Leagues Under the Sea.epub" {
		t.Fatalf("RemotePathFor = %q", got)
	}
	if got := SanitizeName("   "); got != "Unknown" {
		t.Fatalf("blank sanitize = %q", got)
	}
}

func TestPlanBooks(t *testing.T) {
	books := []LibBook{
		{ID: 1, Title: "The Time Machine", Author: "H. G. Wells", Format: "EPUB", SendPath: "/lib/1.epub", SendSize: 100},
		{ID: 2, Title: "Dubliners", Author: "James Joyce", Format: "EPUB", SendPath: "/lib/2.epub", SendSize: 200},
		{ID: 3, Title: "Flatland", Author: "Edwin A. Abbott", Format: "EPUB", SendPath: "/lib/3.epub", SendSize: 300},
	}
	files := []DeviceFile{
		{Path: "/H. G. Wells/The Time Machine.epub", Size: 100},
		{Path: "/Flatland.epub", Size: 305},                // loose in root: title-only match
		{Path: "/Someone Else/Mystery Book.epub", Size: 1}, // orphan
	}
	plan := PlanBooks(books, files)

	if len(plan.OnDevice) != 2 {
		t.Fatalf("OnDevice = %+v", plan.OnDevice)
	}
	byID := map[int64]Match{}
	for _, m := range plan.OnDevice {
		byID[m.BookID] = m
	}
	if m := byID[1]; m.DevicePath != "/H. G. Wells/The Time Machine.epub" {
		t.Fatalf("book 1 match: %+v", m)
	}
	if m := byID[3]; m.DevicePath != "/Flatland.epub" {
		t.Fatalf("book 3 match: %+v", m)
	}
	if len(plan.Missing) != 1 || plan.Missing[0].BookID != 2 {
		t.Fatalf("Missing = %+v", plan.Missing)
	}
	if m := plan.Missing[0]; m.RemotePath != "/James Joyce/Dubliners.epub" {
		t.Fatalf("missing remote path: %+v", m)
	}
	if len(plan.Orphan) != 1 || plan.Orphan[0].Path != "/Someone Else/Mystery Book.epub" {
		t.Fatalf("Orphan = %+v", plan.Orphan)
	}
}

func TestClientEndpointsAndWalk(t *testing.T) {
	f := newFakeDevice()
	f.start()
	defer f.srv.Close()
	ip := strings.TrimPrefix(f.srv.URL, "http://")
	c := New(ip, 81)

	st, err := c.Status(context.Background())
	if err != nil || st.Device != "X3" || st.Mode != "STA" {
		t.Fatalf("Status = %+v err=%v", st, err)
	}

	f.put("/Jules Verne/Journey.epub", 10)
	f.put("/Jules Verne/Notes.txt", 2)
	f.put("/flat.epub", 3)
	got, err := WalkBooks(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 { // .txt filtered out
		t.Fatalf("WalkBooks = %+v", got)
	}

	// walk → plan round trip
	plan := PlanBooks([]LibBook{{ID: 7, Title: "Journey", Author: "Jules Verne", Format: "EPUB"}}, got)
	if len(plan.OnDevice) != 1 || plan.OnDevice[0].DevicePath != "/Jules Verne/Journey.epub" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Orphan) != 1 || plan.Orphan[0].Path != "/flat.epub" {
		t.Fatalf("orphan plan = %+v", plan.Orphan)
	}

	// delete
	if err := c.Delete(context.Background(), []string{"/flat.epub"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.size("/flat.epub"); ok {
		t.Fatal("delete did not remove the file")
	}

	// opds provisioning
	if err := c.SetOPDS(context.Background(), OPDSServer{Name: "manicule", URL: "http://192.168.1.5:8787/opds", Username: "reader"}, "1234"); err != nil {
		t.Fatal(err)
	}
}

func TestWebSocketUpload(t *testing.T) {
	f := newFakeDevice()
	f.start()
	defer f.srv.Close()
	ip := strings.TrimPrefix(f.srv.URL, "http://")
	c := New(ip, 81)
	c.wsURL = "ws://" + ip + "/ws" // test fake serves WS on its own port

	body := bytes.Repeat([]byte("bookblock"), 5000) // 45000 bytes, many chunks
	var lastDone, lastTotal int64
	calls := 0
	err := c.Upload(context.Background(), "/Jules Verne", "Journey.epub",
		bytes.NewReader(body), int64(len(body)),
		func(done, total int64) { lastDone, lastTotal, calls = done, total, calls+1 })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.size("/Jules Verne/Journey.epub"); got != int64(len(body)) {
		t.Fatalf("device received %d bytes, want %d", got, len(body))
	}
	if lastDone != int64(len(body)) || calls < 2 {
		t.Fatalf("progress: done=%d calls=%d", lastDone, calls)
	}
	if lastTotal != int64(len(body)) {
		t.Fatalf("progress total = %d", lastTotal)
	}
}

func TestUploadFallsBackToHTTP(t *testing.T) {
	f := newFakeDevice()
	f.start()
	defer f.srv.Close()
	ip := strings.TrimPrefix(f.srv.URL, "http://")
	c := New(ip, 81)
	c.wsURL = "ws://127.0.0.1:1/" // nothing listening — WS must fail over

	body := []byte("small book")
	err := c.Upload(context.Background(), "/Jane Austen", "Emma.epub",
		bytes.NewReader(body), int64(len(body)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.size("/Jane Austen/Emma.epub"); got != int64(len(body)) {
		t.Fatalf("fallback upload landed %d bytes, want %d", got, len(body))
	}
}
