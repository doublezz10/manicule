package sources

// Shared OPDS 1.x / Atom parsing used by feed-based adapters.

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	RelAcquisition = "http://opds-spec.org/acquisition"
	RelImage       = "http://opds-spec.org/image"
	RelImageThumb  = "http://opds-spec.org/image/thumbnail"
	RelSearch      = "search"
	RelNext        = "next"
	RelSubsection  = "subsection"
	RelStart       = "start"
)

type OPDSLink struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

type OPDSText struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type OPDSAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri"`
}

type OPDSEntry struct {
	ID        string       `xml:"id"`
	Title     string       `xml:"title"`
	Updated   time.Time    `xml:"updated"`
	Published time.Time    `xml:"published"`
	Authors   []OPDSAuthor `xml:"author"`
	Summary   *OPDSText    `xml:"summary"`
	Content   *OPDSText    `xml:"content"`
	Links     []OPDSLink   `xml:"link"`
	Language  string       `xml:"http://purl.org/dc/terms/ language"`
	Issued    string       `xml:"http://purl.org/dc/terms/ issued"`
}

type OPDSFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Links   []OPDSLink  `xml:"link"`
	Entries []OPDSEntry `xml:"entry"`
}

// ParseOPDSFeed decodes an Atom/OPDS document.
func ParseOPDSFeed(r io.Reader) (*OPDSFeed, error) {
	var f OPDSFeed
	if err := xml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("opds: parse feed: %w", err)
	}
	return &f, nil
}

func (e *OPDSEntry) LinkByRel(rel string) *OPDSLink {
	for i := range e.Links {
		if e.Links[i].Rel == rel {
			return &e.Links[i]
		}
	}
	return nil
}

func (e *OPDSEntry) LinksByType(prefix string) []OPDSLink {
	var out []OPDSLink
	for _, l := range e.Links {
		if strings.HasPrefix(l.Type, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func (e *OPDSEntry) Description() string {
	if e.Content != nil && strings.TrimSpace(e.Content.Value) != "" {
		return stripHTML(e.Content.Value)
	}
	if e.Summary != nil {
		return stripHTML(e.Summary.Value)
	}
	return ""
}

func (f *OPDSFeed) LinkByRel(rel string) *OPDSLink {
	for i := range f.Links {
		if f.Links[i].Rel == rel {
			return &f.Links[i]
		}
	}
	return nil
}

var htmlTagReplacer = strings.NewReplacer(
	"<br/>", "\n", "<br />", "\n", "<br>", "\n",
	"</p>", "\n\n", "<div>", "\n", "</div>", "\n",
)

// stripHTML removes markup from OPDS content (type="html" is common).
func stripHTML(s string) string {
	s = htmlTagReplacer.Replace(s)
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// ResolveOpenSearchTemplate follows the rel="search" link of a feed to its
// OpenSearchDescription document and returns the query URL template with a
// {searchTerms} placeholder. Falls back to conventional paths when absent.
func ResolveOpenSearchTemplate(fetch func(url string) (io.ReadCloser, error), feedBase string, root *OPDSFeed) (string, error) {
	if l := root.LinkByRel(RelSearch); l != nil && strings.Contains(l.Type, "opensearchdescription") {
		rc, err := fetch(absoluteLink(l.Href, feedBase))
		if err == nil {
			defer rc.Close()
			var osd struct {
				URLs []struct {
					Type     string `xml:"type,attr"`
					Template string `xml:"template,attr"`
				} `xml:"Url"`
			}
			if err := xml.NewDecoder(rc).Decode(&osd); err == nil {
				for _, u := range osd.URLs {
					if strings.Contains(u.Type, "atom") || strings.Contains(u.Type, "opds") {
						return u.Template, nil
					}
				}
				if len(osd.URLs) > 0 {
					return osd.URLs[0].Template, nil
				}
			}
		}
	}
	// Conventional fallbacks seen across OPDS servers.
	return feedBase + "/search?query={searchTerms}", nil
}
