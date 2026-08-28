package sources

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCanonicalAuthorName(t *testing.T) {
	cases := map[string]string{
		"Doyle, Arthur Conan": "Arthur Conan Doyle",
		"Austen, Jane":        "Jane Austen",
		"Homer":               "Homer",
	}
	for in, want := range cases {
		if got := canonicalAuthorName(in); got != want {
			t.Errorf("canonicalAuthorName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAlephMirror(t *testing.T) {
	got := alephMirror("https://www.gutenberg.org/ebooks/1661.epub3.images")
	want := "https://aleph.gutenberg.org/1/6/6/1661/1661.epub3.images"
	if got != want {
		t.Fatalf("alephMirror = %q, want %q", got, want)
	}
	if alephMirror("https://example.com/foo") != "" {
		t.Fatal("non-gutenberg URL should have no mirror")
	}
}

func TestFormatFromMime(t *testing.T) {
	if f := formatFromMime("application/epub+zip", "https://x/y.epub"); f == nil || f.Name != "EPUB" {
		t.Fatalf("epub not recognized: %+v", f)
	}
	if f := formatFromMime("text/plain; charset=us-ascii", "https://x/y.txt"); f != nil {
		t.Fatalf("plain text should be skipped: %+v", f)
	}
}

func TestPreferFormat(t *testing.T) {
	formats := []Format{{Name: "MOBI"}, {Name: "EPUB"}, {Name: "TXT"}}
	pick := PreferFormat(formats)
	if pick == nil || pick.Name != "EPUB" {
		t.Fatalf("expected EPUB preferred, got %+v", pick)
	}
}

func TestZLibraryNeedsAuth(t *testing.T) {
	z := NewZLibrary(nil).(*zlibrary)
	if !z.NeedsAuth() {
		t.Fatal("expected NeedsAuth true with no credentials")
	}
	z.SetCredentials(Credentials{"email": "a@b.com", "password": "pass"})
	if z.NeedsAuth() {
		t.Fatal("expected NeedsAuth false after setting credentials")
	}
}

func TestZLibrarySessionInvalidation(t *testing.T) {
	z := NewZLibrary(nil).(*zlibrary)
	z.SetCredentials(Credentials{"email": "a@b.com", "password": "pass"})
	z.mu.Lock()
	z.session = "old-session"
	z.mu.Unlock()

	// Changing credentials should clear session
	z.SetCredentials(Credentials{"email": "new@b.com", "password": "pass2"})
	z.mu.Lock()
	s := z.session
	z.mu.Unlock()
	if s != "" {
		t.Fatalf("session not cleared after credential change: %q", s)
	}
}

func TestZLibraryBaseURLSessionInvalidation(t *testing.T) {
	z := NewZLibrary(nil).(*zlibrary)
	z.SetBaseURL("https://mirror1.com")
	z.mu.Lock()
	z.session = "session1"
	z.mu.Unlock()

	// Changing mirror should clear session
	z.SetBaseURL("https://mirror2.com")
	z.mu.Lock()
	s := z.session
	z.mu.Unlock()
	if s != "" {
		t.Fatalf("session not cleared after mirror change: %q", s)
	}
}

func TestZLibraryResolveCover(t *testing.T) {
	z := NewZLibrary(nil).(*zlibrary)
	z.SetBaseURL("https://z-lib.gs")

	if got := z.resolveCover(""); got != "" {
		t.Fatalf("empty cover should return empty, got %q", got)
	}
	if got := z.resolveCover("https://other.com/c.jpg"); got != "https://other.com/c.jpg" {
		t.Fatalf("absolute cover should pass through, got %q", got)
	}
	if got := z.resolveCover("/covers/123.jpg"); got != "https://z-lib.gs/covers/123.jpg" {
		t.Fatalf("relative cover should resolve, got %q", got)
	}
}

func TestOpenLibraryMetadata(t *testing.T) {
	ol := NewOpenLibrary(nil)
	if ol.ID() != "openlibrary" {
		t.Fatalf("ID = %q", ol.ID())
	}
	if ol.Tier() != 1 {
		t.Fatalf("Tier = %d, want 1", ol.Tier())
	}
	if ol.NeedsAuth() {
		t.Fatal("OL should not need auth")
	}
}

func TestOpenLibraryDownloadUnsupported(t *testing.T) {
	ol := NewOpenLibrary(nil)
	err := ol.Download(nil, Format{}, nil, nil)
	if err == nil {
		t.Fatal("expected error for unsupported download")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCopyWithProgress(t *testing.T) {
	var calls []int64
	r := strings.NewReader(strings.Repeat("x", 5000))
	var dst bytes.Buffer
	err := CopyWithProgress(&dst, r, 5000, func(done, total int64) {
		calls = append(calls, done)
		if total != 5000 {
			t.Fatalf("total = %d, want 5000", total)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if dst.Len() != 5000 {
		t.Fatalf("copied %d bytes, want 5000", dst.Len())
	}
	if len(calls) < 2 || calls[0] != 0 || calls[len(calls)-1] != 5000 {
		t.Fatalf("progress sequence wrong: first=%v last=%v n=%d", calls[0], calls[len(calls)-1], len(calls))
	}
}

func TestGutendexDownloadProgress(t *testing.T) {
	body := strings.Repeat("epub", 1250) // 5000 bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5000")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	g := NewGutendex(nil).(*gutendex)
	g.base = srv.URL // download URLs point at the test server
	var lastDone, lastTotal int64
	calls := 0
	err := g.Download(context.Background(), Format{URL: srv.URL + "/book.epub"}, io.Discard,
		func(done, total int64) { lastDone, lastTotal, calls = done, total, calls+1 })
	if err != nil {
		t.Fatal(err)
	}
	if lastDone != 5000 || lastTotal != 5000 || calls < 2 {
		t.Fatalf("progress: done=%d total=%d calls=%d", lastDone, lastTotal, calls)
	}
}

func TestStandardEbooksTemplateCache(t *testing.T) {
	rootFeed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:opds="http://opds-spec.org/2010/catalog">
  <id>urn:se:root</id><title>Standard Ebooks</title>
  <link rel="search" type="application/opensearchdescription+xml" href="opensearch.xml"/>
</feed>`
	osd := `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <Url type="application/atom+xml;profile=opds-catalog" template="/feeds/search/{searchTerms}"/>
</OpenSearchDescription>`
	searchFeed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:se:search</id><title>Search</title>
  <entry><id>urn:uuid:abc-123</id><title>Jane Eyre</title>
    <author><name>Charlotte Brontë</name></author>
    <link rel="http://opds-spec.org/acquisition" type="application/epub+zip" href="/ebooks/jane-eyre.epub"/>
  </entry>
</feed>`

	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		w.Header().Set("Content-Type", "application/atom+xml")
		switch r.URL.Path {
		case "/feeds/opds":
			w.Write([]byte(rootFeed))
		case "/feeds/opds/opensearch.xml":
			w.Write([]byte(osd))
		default:
			w.Write([]byte(searchFeed))
		}
	}))
	defer srv.Close()

	s := NewStandardEbooks(nil).(*standardebooks)
	s.base = srv.URL
	s.SetCredentials(Credentials{"email": "patron@example.com"})

	for i := 0; i < 2; i++ {
		res, err := s.Search(context.Background(), "bronte", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Title != "Jane Eyre" {
			t.Fatalf("search %d: %+v", i, res)
		}
	}
	if hits["/feeds/opds"] != 1 || hits["/feeds/opds/opensearch.xml"] != 1 {
		t.Fatalf("template should be fetched once: %v", hits)
	}
}
