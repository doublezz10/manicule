// Package opds serves the library as an OPDS 1.2 catalog over plain http —
// https OOM-crashes e-ink readers (§4.1 of the spec). Port 8787, page size
// 20, auth ON by default with tiny credentials: username "reader", 4-char PIN.
package opds

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/doublezz10/manicule/internal/library"
)

const (
	PageLimit = 20
	Username  = "reader"
)

type Server struct {
	mu          sync.Mutex
	store       *library.Store
	port        int
	authEnabled bool
	pin         string
	http        *http.Server
	started     time.Time
}

func New(store *library.Store, port int, authEnabled bool, pin string) *Server {
	s := &Server{store: store, port: port, authEnabled: authEnabled}
	s.pin = pin
	return s
}

// UpdateAuth applies credential changes live.
func (s *Server) UpdateAuth(enabled bool, pin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authEnabled = enabled
	s.pin = pin
}

func (s *Server) Auth() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authEnabled, s.pin
}

func (s *Server) Addr() int { return s.port }

func (s *Server) StartedAt() time.Time { return s.started }

// Start binds all interfaces and serves until Stop. Non-blocking.
func (s *Server) Start() error {
	if s.http != nil {
		return fmt.Errorf("opds: already running")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/opds", s.handleRoot)
	mux.HandleFunc("/opds/", s.handleOpdsSub)
	mux.HandleFunc("/opds/search.xml", s.handleOpenSearch)
	mux.HandleFunc("/download/", s.handleDownload)
	mux.HandleFunc("/cover/", s.handleCover)

	addr := fmt.Sprintf(":%d", s.port)
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.logMW(s.authMW(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.started = time.Now()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.http = nil
		return fmt.Errorf("opds: bind %s: %w", addr, err)
	}
	go func() {
		if err := s.http.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("opds server stopped", "err", err)
		}
	}()
	slog.Info("opds: serving", "addr", addr)
	return nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.http
	s.http = nil
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := contextWithTimeout(3 * time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}
}

func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.http != nil
}

// --- middleware ------------------------------------------------------------

func (s *Server) logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("opds request", "path", r.URL.Path, "ua", r.UserAgent())
		next.ServeHTTP(w, r)
	})
}

// authMW enforces tiny-credential Basic auth when enabled. OPDS clients on
// the device send credentials per saved server entry (§4.3).
func (s *Server) authMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authOn, pin := s.Auth()
		if !authOn || strings.HasPrefix(r.URL.Path, "/cover/") {
			// Covers stay public: they're thumbnails of free/public-domain
			// books and must render inside the app's own webview.
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		userOK := ok && subtle.ConstantTimeCompare([]byte(user), []byte(Username)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(pin)) == 1
		if !(userOK && passOK) {
			w.Header().Set("WWW-Authenticate", `Basic realm="manicule", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- routes ----------------------------------------------------------------

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/opds" {
		http.NotFound(w, r)
		return
	}
	writeXML(w, navFeed("manicule", "", navEntryList(
		navEntry("Newest", "Newest additions to your library", "/opds/newest"),
		navEntry("By Title", "Browse alphabetically", "/opds/by-title"),
		navEntry("By Author", "Browse by author", "/opds/by-author"),
	)))
}

func (s *Server) handleOpdsSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/opds/")
	page := pageOf(r)
	switch {
	case path == "newest":
		books, _ := s.store.Newest(page*PageLimit, PageLimit)
		total, _ := s.store.Count()
		writeXML(w, acquisitionFeed(s.base(r), "Newest", books, page, total))
	case path == "by-title":
		books, _ := s.store.List("", "title", page*PageLimit, PageLimit)
		total, _ := s.store.Count()
		writeXML(w, acquisitionFeed(s.base(r), "By Title", books, page, total))
	case path == "by-author":
		s.handleAuthors(w, r)
	case strings.HasPrefix(path, "by-author/"):
		name, err := urlUnescape(strings.TrimPrefix(path, "by-author/"))
		if err != nil || name == "" {
			http.NotFound(w, r)
			return
		}
		books, _ := s.store.ByAuthor(name, page*PageLimit, PageLimit)
		writeXML(w, acquisitionFeed(s.base(r), name, books, page, len(books)))
	case path == "search":
		q := strings.TrimSpace(r.URL.Query().Get("query"))
		if q == "" {
			q = strings.TrimSpace(r.URL.Query().Get("q"))
		}
		if q == "" {
			writeXML(w, acquisitionFeed(s.base(r), "Search", nil, 0, 0))
			return
		}
		books, _ := s.store.List(q, "recent", page*PageLimit, PageLimit)
		writeXML(w, acquisitionFeed(s.base(r), `Results for "`+q+`"`, books, page, len(books)))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAuthors(w http.ResponseWriter, r *http.Request) {
	authors, err := s.store.Authors()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var entries []navEntryT
	for _, a := range authors {
		entries = append(entries, navEntry(a, "Books by "+a,
			"/opds/by-author/"+urlPathEscape(a)))
	}
	writeXML(w, navFeed("By Author", "", entries))
}

// handleDownload streams the chosen file with a friendly filename. The X3's
// downloader fetches acquisition hrefs directly with stored creds (§4.3).
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/download/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	bookID, err1 := strconv.ParseInt(parts[0], 10, 64)
	fileID, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		http.Error(w, "bad ids", http.StatusBadRequest)
		return
	}
	bw, err := s.store.GetBook(bookID)
	if err != nil || bw == nil {
		http.NotFound(w, r)
		return
	}
	var file *library.BookFile
	for i := range bw.Files {
		if bw.Files[i].ID == fileID {
			file = &bw.Files[i]
			break
		}
	}
	if file == nil {
		http.NotFound(w, r)
		return
	}
	f, err := openFile(s.store.AbsPath(file.Path))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	title := sanitizeFilename(bw.Book.Title)
	if !file.IsOriginal {
		title += ".clean"
	}
	w.Header().Set("Content-Type", mime.TypeByExtension("."+extLower(file.Path)))
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.%s"`, title, strings.ToLower(file.Format)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", file.SizeBytes))
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/cover/"), ".jpg")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	bw, err := s.store.GetBook(id)
	if err != nil || bw == nil || bw.Book.CoverPath == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, s.store.AbsPath(bw.Book.CoverPath))
}

func (s *Server) base(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = "localhost:" + strconv.Itoa(s.port)
	}
	return "http://" + host
}

// --- OpenSearch ------------------------------------------------------------

func (s *Server) handleOpenSearch(w http.ResponseWriter, r *http.Request) {
	base := s.base(r)
	writeXML(w, []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>manicule</ShortName>
  <Description>Search your manicule library</Description>
  <InputEncoding>UTF-8</InputEncoding>
  <OutputEncoding>UTF-8</OutputEncoding>
  <Url type="application/atom+xml;profile=opds-catalog:kind:acquisition" template="%s/opds/search?query={searchTerms}"/>
</OpenSearchDescription>`, base)))
}

// LANURL returns the first non-loopback IPv4 URL for QR + tray display.
func LANURL(port int) string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return fmt.Sprintf("http://localhost:%d/opds", port)
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			if v4 := ipn.IP.To4(); v4 != nil {
				return fmt.Sprintf("http://%s:%d/opds", v4.String(), port)
			}
		}
	}
	return fmt.Sprintf("http://localhost:%d/opds", port)
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
