package opds

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/doublezz10/manicule/internal/config"
	"github.com/doublezz10/manicule/internal/library"
)

func TestNavFeedStructure(t *testing.T) {
	xmlBytes := navFeed("manicule", "", navEntryList(
		navEntry("Newest", "Newest additions", "/opds/newest"),
	))
	var f struct {
		XMLName xml.Name `xml:"feed"`
		Entries []struct {
			Title string `xml:"title"`
			Links []struct {
				Rel  string `xml:"rel,attr"`
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(xmlBytes, &f); err != nil {
		t.Fatalf("nav feed not valid XML: %v\n%s", err, xmlBytes)
	}
	if len(f.Entries) != 1 || f.Entries[0].Title != "Newest" {
		t.Fatalf("entries wrong: %+v", f.Entries)
	}
	if f.Entries[0].Links[0].Href != "/opds/newest" {
		t.Fatalf("subsection href wrong")
	}
}

func TestAcquisitionFeedFormats(t *testing.T) {
	books := []library.BookWithFiles{{
		Book: library.Book{
			ID: 7, Title: "Emma & <Friends>", Authors: []string{"Jane Austen"},
			CoverPath: ".manicule/covers/7.jpg",
		},
		Files: []library.BookFile{
			{ID: 11, Format: "EPUB", Path: "x.epub", IsOriginal: true},
			{ID: 12, Format: "EPUB", Path: "x.clean.epub", IsOriginal: false},
		},
	}}
	out := acquisitionFeed("http://192.168.1.5:8787", "Newest", books, 0, 1)
	s := string(out)
	if !strings.Contains(s, "application/epub+zip") {
		t.Error("epub media type missing")
	}
	if !strings.Contains(s, "/download/7/11") {
		t.Error("master acquisition link missing")
	}
	if !strings.Contains(s, "/download/7/12") {
		t.Error("clean derivative link missing")
	}
	if !strings.Contains(s, "&amp;") {
		t.Error("XML escaping missing in title")
	}
	if !strings.Contains(s, "e-ink cleaned") {
		t.Error("derivative label missing")
	}
	var v any
	if err := xml.Unmarshal(out, &v); err != nil {
		t.Fatalf("acquisition feed not valid XML: %v", err)
	}
}

func TestProvisionJSON(t *testing.T) {
	data, err := ProvisionJSON("http://192.168.1.5:8787/opds", "reader", "4271")
	if err != nil {
		t.Fatal(err)
	}
	var servers []CrosspointServer
	if err := json.Unmarshal(data, &servers); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	s := servers[0]
	if s.Name != "manicule" || s.Username != "reader" || s.Password != "4271" ||
		s.URL != "http://192.168.1.5:8787/opds" {
		t.Fatalf("provisioning shape wrong: %+v", s)
	}
}

func TestPINGeneration(t *testing.T) {
	pin := config.GeneratePIN()
	if len(pin) != 4 {
		t.Fatalf("pin length %d", len(pin))
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			t.Fatalf("pin %q has non-digit", pin)
		}
	}
}
