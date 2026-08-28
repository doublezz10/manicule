package sources

// Standard Ebooks adapter over their OPDS 1.2 feed.
//
// VERIFIED 2026-08-21: the full OPDS catalog is now a Patrons Circle benefit.
// Access = patron email as username + blank password (HTTP Basic). Without
// credentials the feed returns 401 and this adapter reports "needs-auth".
// The New Releases RSS/Atom feed remains anonymous but is not searchable,
// so search requires the user's email.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type standardebooks struct {
	client     *http.Client
	base       string // e.g. https://standardebooks.org
	feed       string // path to OPDS root, default /feeds/opds
	creds      Credentials
	lastStatus Status

	tmplMu      sync.Mutex
	searchTmpl  string    // cached OpenSearch URL template
	tmplSavedAt time.Time // zero until first successful resolve
}

func NewStandardEbooks(client *http.Client) Source {
	if client == nil {
		client = NewHTTPClient()
	}
	return &standardebooks{
		client: client,
		base:   "https://standardebooks.org",
		feed:   "/feeds/opds",
	}
}

func (s *standardebooks) ID() string   { return "standardebooks" }
func (s *standardebooks) Name() string { return "Standard Ebooks" }
func (s *standardebooks) Tier() int    { return 1 }

// NeedsAuth is true until the user supplies a Patrons Circle email. SE's
// free anonymous tier covers only the unsearchable new-releases feed, so a
// credential-less source cannot deliver its core value.
func (s *standardebooks) NeedsAuth() bool { return s.creds["email"] == "" }

func (s *standardebooks) SetCredentials(c Credentials) {
	if c == nil {
		c = Credentials{}
	}
	s.creds = c
}

func (s *standardebooks) SetBaseURL(base string) {
	if base != "" {
		s.base = strings.TrimRight(base, "/")
	}
}

func (s *standardebooks) Status() Status {
	if s.lastStatus.SourceID == "" {
		return Status{SourceID: s.ID(), State: "ready"}
	}
	return s.lastStatus
}

func (s *standardebooks) fetchFeed(ctx context.Context, feedURL string) (*OPDSFeed, error) {
	resp, err := httpGet(ctx, s.client, feedURL, &s.creds)
	if err != nil {
		return nil, fmt.Errorf("standardebooks: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrNeedsAuth
	default:
		return nil, fmt.Errorf("standardebooks: HTTP %d fetching %s", resp.StatusCode, feedURL)
	}
	return ParseOPDSFeed(resp.Body)
}

// searchTemplate returns the OpenSearch URL template, caching it for an hour.
// Resolving it fresh costs two sequential round trips (root feed, then the
// description document) before the actual search — the cache turns every
// repeat search into a single request.
func (s *standardebooks) searchTemplate(ctx context.Context) (string, error) {
	s.tmplMu.Lock()
	tmpl, age := s.searchTmpl, time.Since(s.tmplSavedAt)
	s.tmplMu.Unlock()
	if tmpl != "" && age < time.Hour {
		return tmpl, nil
	}

	root, err := s.fetchFeed(ctx, s.base+s.feed)
	if err != nil {
		return "", err
	}
	tmpl, _ = ResolveOpenSearchTemplate(func(u string) (io.ReadCloser, error) {
		resp, err := httpGet(ctx, s.client, u, &s.creds)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, u)
		}
		return resp.Body, nil
	}, s.base+s.feed, root)
	if tmpl == "" {
		tmpl = s.base + s.feed + "/search?query={searchTerms}"
	}
	s.tmplMu.Lock()
	s.searchTmpl = tmpl
	s.tmplSavedAt = time.Now()
	s.tmplMu.Unlock()
	return tmpl, nil
}

func (s *standardebooks) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" || s.NeedsAuth() {
		return nil, ErrNeedsAuth
	}
	tmpl, err := s.searchTemplate(ctx)
	if err != nil {
		return nil, err
	}
	searchURL := strings.Replace(tmpl, "{searchTerms}", url.QueryEscape(query), 1)
	feed, err := s.fetchFeed(ctx, absoluteLink(searchURL, s.base))
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		r := Result{
			SourceID:    s.ID(),
			SourceName:  s.Name(),
			ID:          entryKey(e.ID),
			Title:       e.Title,
			Description: e.Description(),
			CoverURL:    absoluteLink(linkHref(&e, RelImageThumb), s.base),
			Language:    e.Language,
		}
		if !e.Published.IsZero() {
			r.Year = fmt.Sprintf("%d", e.Published.Year())
		} else if e.Issued != "" {
			r.Year = e.Issued
		}
		for _, a := range e.Authors {
			r.Authors = append(r.Authors, a.Name)
		}
		for _, l := range e.LinksByType("application/epub+zip") {
			r.Formats = append(r.Formats, Format{Name: "EPUB", URL: absoluteLink(l.Href, s.base), Size: l.Length})
			break
		}
		for _, l := range e.LinksByType("application/x-mobipocket-ebook") {
			r.Formats = append(r.Formats, Format{Name: "MOBI", URL: absoluteLink(l.Href, s.base), Size: l.Length})
			break
		}
		if len(r.Formats) > 0 {
			out = append(out, r)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	s.lastStatus = Status{SourceID: s.ID(), State: "ready"}
	return out, nil
}

func (s *standardebooks) Download(ctx context.Context, f Format, w io.Writer, onProgress ProgressFunc) error {
	err := streamTo(ctx, s.client, f.URL, &s.creds, w, onProgress)
	if err != nil {
		s.lastStatus = Status{SourceID: s.ID(), State: "error", Message: err.Error()}
		return err
	}
	return nil
}

func linkHref(e *OPDSEntry, rel string) string {
	if l := e.LinkByRel(rel); l != nil {
		return l.Href
	}
	return ""
}

// entryKey reduces an OPDS entry id (often a urn or full URL) to something stable.
func entryKey(id string) string {
	id = strings.TrimPrefix(id, "urn:uuid:")
	if i := strings.LastIndexByte(id, '/'); i >= 0 && i < len(id)-1 {
		id = id[i+1:]
	}
	return strings.TrimSuffix(id, ".epub")
}

// absoluteLink resolves href against base when relative.
func absoluteLink(href, base string) string {
	if href == "" || strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return base + href
}
