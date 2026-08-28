package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Gutendex adapter: Project Gutenberg metadata via the Gutendex JSON API;
// downloads go straight to gutenberg.org (or mirror) file URLs from the
// formats map. Anonymous, no auth.

type gutendex struct {
	client *http.Client
	base   string
}

func NewGutendex(client *http.Client) Source {
	if client == nil {
		client = NewHTTPClient()
	}
	return &gutendex{client: client, base: "https://gutendex.com"}
}

func (g *gutendex) ID() string   { return "gutendex" }
func (g *gutendex) Name() string { return "Project Gutenberg" }
func (g *gutendex) Tier() int    { return 1 }
func (g *gutendex) NeedsAuth() bool {
	return false
}
func (g *gutendex) SetCredentials(Credentials) {}
func (g *gutendex) SetBaseURL(base string) {
	if base != "" {
		g.base = strings.TrimRight(base, "/")
	}
}
func (g *gutendex) Status() Status { return Status{SourceID: g.ID(), State: "ready"} }

type gutendexAuthor struct {
	Name string `json:"name"`
}

type gutendexBook struct {
	ID        int               `json:"id"`
	Title     string            `json:"title"`
	Authors   []gutendexAuthor  `json:"authors"`
	Summaries []string          `json:"summaries"`
	Languages []string          `json:"languages"`
	Cover     string            `json:"-"` // from formats
	Formats   map[string]string `json:"formats"`
}

func (g *gutendex) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	// Gutendex requires a trailing slash on /books/ (verified 2026-08-21).
	u := fmt.Sprintf("%s/books/?search=%s", g.base, url.QueryEscape(query))
	resp, err := httpGet(ctx, g.client, u, nil)
	if err != nil {
		return nil, fmt.Errorf("gutendex: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound && strings.HasSuffix(u, "/books/?") {
		// Older Gutendex deployments accept /books without slash.
		resp.Body.Close()
		resp, err = httpGet(ctx, g.client, strings.TrimSuffix(u, "/"), nil)
		if err != nil {
			return nil, fmt.Errorf("gutendex: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gutendex: HTTP %d", resp.StatusCode)
	}
	var page struct {
		Count   int            `json:"count"`
		Results []gutendexBook `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("gutendex: decode: %w", err)
	}
	out := make([]Result, 0, len(page.Results))
	for _, b := range page.Results {
		r := Result{
			SourceID:    g.ID(),
			SourceName:  g.Name(),
			ID:          fmt.Sprintf("pg-%d", b.ID),
			Title:       b.Title,
			Description: firstNonEmpty(b.Summaries...),
			CoverURL:    b.Formats["image/jpeg"],
		}
		if len(b.Languages) > 0 {
			r.Language = b.Languages[0]
		}
		for _, a := range b.Authors {
			r.Authors = append(r.Authors, canonicalAuthorName(a.Name))
		}
		for mime, dl := range b.Formats {
			f := formatFromMime(mime, dl)
			if f == nil {
				continue
			}
			r.Formats = append(r.Formats, *f)
		}
		if len(r.Formats) > 0 {
			out = append(out, r)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Download streams the file. Gutenberg geo-blocks Germany; fleet download-host
// failover rewrites www.gutenberg.org URLs to aleph.gutenberg.org pub paths.
func (g *gutendex) Download(ctx context.Context, f Format, w io.Writer, onProgress ProgressFunc) error {
	candidates := []string{f.URL}
	if alt := alephMirror(f.URL); alt != "" {
		candidates = append(candidates, alt)
	}
	var lastErr error
	for _, c := range candidates {
		err := streamTo(ctx, g.client, c, nil, w, onProgress)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

// alephMirror converts https://www.gutenberg.org/ebooks/1661.epub3.images to
// https://aleph.gutenberg.org/1/6/6/1661/1661.epub3.images when parseable.
func alephMirror(u string) string {
	const prefix = "https://www.gutenberg.org/ebooks/"
	if !strings.HasPrefix(u, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(u, prefix) // e.g. "1661.epub3.images" or "1661/1661.epub3.images"
	id := rest
	if i := strings.IndexByte(rest, '.'); i > 0 {
		id = rest[:i]
	}
	if id == "" {
		return ""
	}
	var path strings.Builder
	for _, r := range id[:len(id)-1] { // all but last digit becomes nested dirs
		path.WriteRune(r)
		path.WriteByte('/')
	}
	path.WriteString(id + "/")
	file := rest
	if !strings.Contains(file, "/") {
		file = id + "/" + file
	}
	return "https://aleph.gutenberg.org/" + path.String() + file[strings.Index(file, "/")+1:]
}

func formatFromMime(mime, dl string) *Format {
	switch {
	case strings.HasPrefix(mime, "application/epub+zip"):
		return &Format{Name: "EPUB", URL: dl}
	case strings.HasPrefix(mime, "application/x-mobipocket-ebook"):
		return &Format{Name: "MOBI", URL: dl}
	case strings.HasPrefix(mime, "text/plain"), strings.HasPrefix(mime, "application/pdf"),
		strings.HasPrefix(mime, "text/html"), strings.HasPrefix(mime, "application/rdf+xml"):
		return nil // not useful for e-reader ingestion
	default:
		return nil
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// canonicalAuthorName converts "Doyle, Arthur Conan" → "Arthur Conan Doyle".
func canonicalAuthorName(name string) string {
	if i := strings.IndexByte(name, ','); i > 0 {
		last := strings.TrimSpace(name[:i])
		rest := strings.TrimSpace(name[i+1:])
		if rest != "" {
			return rest + " " + last
		}
		return last
	}
	return name
}

func streamTo(ctx context.Context, client *http.Client, url string, creds *Credentials, w io.Writer, onProgress ProgressFunc) error {
	resp, err := httpGet(ctx, client, url, creds)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return CopyWithProgress(w, resp.Body, resp.ContentLength, onProgress)
}
