package opds

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doublezz10/manicule/internal/library"
)

func TestServerAuthAndFeeds(t *testing.T) {
	s, _ := library.Open(t.TempDir())
	t.Cleanup(func() { s.Close() })

	if _, err := s.AddBook(
		&library.Book{Title: "Emma", Authors: []string{"Jane Austen"}},
		&library.BookFile{Format: "EPUB", Path: "Austen/Emma/emma.epub", SizeBytes: 3},
	); err != nil {
		t.Fatal(err)
	}

	srv := New(s, 8787, true, "4271")
	handler := srv.authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/opds":
			writeXML(w, navFeed("root", "", nil))
		case "/opds/newest":
			books, _ := s.Newest(0, PageLimit)
			writeXML(w, acquisitionFeed("http://x:8787", "Newest", books, 0, len(books)))
		default:
			http.NotFound(w, r)
		}
	}))

	// No credentials → 401 + WWW-Authenticate.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/opds", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate challenge")
	}

	// Wrong PIN → 401.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/opds", nil)
	req.SetBasicAuth(Username, "0000")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong PIN got %d", rec.Code)
	}

	// Correct tiny creds → root feed.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/opds", nil)
	req.SetBasicAuth(Username, "4271")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("authenticated root feed failed: %d", rec.Code)
	}

	// Newest acquisition feed lists the book's acquisition link.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/opds/newest", nil)
	req.SetBasicAuth(Username, "4271")
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !contains(body, "Emma") || !contains(body, "Jane Austen") {
		t.Fatalf("book missing from newest feed:\n%s", body)
	}
	if !contains(body, "/download/1/1") {
		t.Fatalf("acquisition link missing:\n%s", body)
	}
}

func contains(hay, needle string) bool { return strings.Contains(hay, needle) }
