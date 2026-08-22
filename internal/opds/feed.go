package opds

// OPDS 1.2 feed rendering (Atom XML built directly — no template escaping
// surprises). Acquisition entries expose every format the library holds,
// including the derived clean EPUB (dual-format entries answer X3 issue
// #2525/#2522: optimize-on-download, answered at the catalog level).

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/doublezz10/manicule/internal/library"
)

func writeXML(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8; profile=opds-catalog:kind:navigation")
	w.Write(data)
}

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
)

func esc(s string) string { return xmlEscaper.Replace(s) }

type linkT struct {
	rel  string
	href string
	typ  string
}

type navEntryT struct {
	id    string
	title string
	desc  string
	href  string
}

func navEntry(title, desc, href string) navEntryT {
	return navEntryT{id: href, title: title, desc: desc, href: href}
}

func navEntryList(entries ...navEntryT) []navEntryT { return entries }

func navFeed(title, subtitle string, entries []navEntryT) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:opds="http://opds-spec.org/2010/catalog">
  <id>urn:manicule:%s</id>
  <title>%s</title>
`, esc(slug(title)), esc(title))
	if subtitle != "" {
		fmt.Fprintf(&b, `  <subtitle>%s</subtitle>`+"\n", esc(subtitle))
	}
	b.WriteString(`  <updated>` + time.Now().UTC().Format(time.RFC3339) + "</updated>\n")
	b.WriteString(`  <author><name>manicule</name><uri>http://opds-spec.org</uri></author>` + "\n")
	for _, e := range entries {
		fmt.Fprintf(&b, `  <entry>
    <id>urn:manicule:%s</id>
    <title>%s</title>
    <updated>%s</updated>
`, esc(slug(e.id)), esc(e.title), time.Now().UTC().Format(time.RFC3339))
		if e.desc != "" {
			fmt.Fprintf(&b, `    <content type="text">%s</content>`+"\n", esc(e.desc))
		}
		fmt.Fprintf(&b, `    <link rel="subsection" type="application/atom+xml;profile=opds-catalog:kind=navigation" href="%s"/>
  </entry>
`, esc(e.href))
	}
	b.WriteString("</feed>\n")
	return b.Bytes()
}

// acquisitionFeed renders a paged acquisition feed with per-format links.
func acquisitionFeed(base, title string, books []library.BookWithFiles, page, total int) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:opds="http://opds-spec.org/2010/catalog" xmlns:pse="http://opds-spec.org/2010/page-turn">
  <id>urn:manicule:%s</id>
  <title>%s</title>
  <updated>%s</updated>
`, esc(slug(title)), esc(title), time.Now().UTC().Format(time.RFC3339))
	if page > 0 {
		fmt.Fprintf(&b, `  <link rel="prev" type="application/atom+xml;profile=opds-catalog:kind=acquisition" href="/opds/newest?page=%d"/>`+"\n", page-1)
	}
	if (page+1)*PageLimit < total {
		fmt.Fprintf(&b, `  <link rel="next" type="application/atom+xml;profile=opds-catalog:kind=acquisition" href="/opds/newest?page=%d"/>`+"\n", page+1)
	}

	for i := range books {
		bk := &books[i]
		selfHref := fmt.Sprintf("/download/%d", bk.Book.ID)
		fmt.Fprintf(&b, `  <entry>
    <id>urn:manicule:book-%d</id>
    <title>%s</title>
    <updated>%s</updated>
`, bk.Book.ID, esc(bk.Book.Title), bk.Book.AddedAt.UTC().Format(time.RFC3339))
		for _, a := range bk.Book.Authors {
			fmt.Fprintf(&b, `    <author><name>%s</name></author>`+"\n", esc(a))
		}
		if bk.Book.Description != "" {
			desc := bk.Book.Description
			if len(desc) > 1200 {
				desc = desc[:1200] + "…"
			}
			fmt.Fprintf(&b, `    <summary type="text">%s</summary>`+"\n", esc(desc))
		}
		if bk.Book.CoverPath != "" {
			fmt.Fprintf(&b, `    <link rel="http://opds-spec.org/image" type="%s" href="%s/cover/%d.jpg"/>`+"\n",
				imageMime(bk.Book.CoverPath), base, bk.Book.ID)
			fmt.Fprintf(&b, `    <link rel="http://opds-spec.org/image/thumbnail" type="%s" href="%s/cover/%d.jpg"/>`+"\n",
				imageMime(bk.Book.CoverPath), base, bk.Book.ID)
		}
		for _, f := range bk.Files {
			mt := mimeFor(f.Format)
			if mt == "" {
				continue
			}
			label := ""
			if !f.IsOriginal {
				label = " (e-ink cleaned)"
			}
			fmt.Fprintf(&b, `    <link rel="http://opds-spec.org/acquisition" type="%s" title="%s%s" href="%s/%d"/>`+"\n",
				mt, esc(strings.ToUpper(f.Format)), esc(label), selfHref, f.ID)
		}
		b.WriteString("  </entry>\n")
	}
	b.WriteString("</feed>\n")
	return b.Bytes()
}

func mimeFor(format string) string {
	switch strings.ToLower(format) {
	case "epub":
		return "application/epub+zip"
	case "mobi":
		return "application/x-mobipocket-ebook"
	case "azw3":
		return "application/vnd.amazon.ebook"
	case "kepub":
		return "application/kepub+zip"
	case "xtch":
		return "application/octet-stream"
	default:
		return ""
	}
}

func imageMime(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".png":
		return "image/png"
	default:
		return "image/jpeg"
	}
}

var slugStrip = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

func slug(s string) string {
	s = strings.ReplaceAll(s, " ", "-")
	return strings.Trim(slugStrip.ReplaceAllString(s, ""), "-")
}

func sanitizeFilename(name string) string {
	replaced := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		return r
	}, name)
	return strings.TrimSpace(replaced)
}

// small indirections keep the file honest about stdlib usage
func pageOf(r *http.Request) int {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if p < 0 {
		return 0
	}
	return p
}

func urlUnescape(s string) (string, error) { return url.PathUnescape(s) }
func urlPathEscape(s string) string        { return url.PathEscape(s) }
func extLower(p string) string             { return strings.ToLower(filepath.Ext(p)) }
func openFile(p string) (*os.File, error)  { return os.Open(p) }
