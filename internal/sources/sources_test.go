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
