package sources

import "testing"

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
