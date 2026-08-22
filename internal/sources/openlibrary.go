package sources

// Open Library adapter — metadata layer (§5.1). Primary value: rich covers,
// descriptions, and subject tags. Acquisition is de-prioritized per spec;
// OL search results have no download formats in v1. The cover enrichment
// utility (EnrichCover) is also used by the ingest pipeline to backfill
// missing cover art from other sources.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	olSearchBase = "https://openlibrary.org"
	olCoversBase = "https://covers.openlibrary.org"
)

type openlibrary struct {
	client *http.Client
}

func NewOpenLibrary(client *http.Client) Source {
	if client == nil {
		client = NewHTTPClient()
	}
	return &openlibrary{client: client}
}

// --- OL API response types ---

type olSearchResponse struct {
	NumFound int      `json:"numFound"`
	Docs     []olDoc  `json:"docs"`
}

type olDoc struct {
	Key              string   `json:"key"`                // /works/OL12345W
	Title            string   `json:"title"`
	AuthorName       []string `json:"author_name"`
	FirstPublishYear int      `json:"first_publish_year"`
	CoverI           int      `json:"cover_i"`            // cover image ID
	Language         []string `json:"language"`
	Subject          []string `json:"subject"`
	IA               []string `json:"ia"`                 // Internet Archive IDs
	HasFulltext      bool     `json:"has_fulltext"`
}

// --- Source interface ---

func (o *openlibrary) ID() string   { return "openlibrary" }
func (o *openlibrary) Name() string { return "Open Library" }
func (o *openlibrary) Tier() int    { return 1 }

func (o *openlibrary) NeedsAuth() bool { return false }
func (o *openlibrary) SetCredentials(Credentials) {}
func (o *openlibrary) SetBaseURL(base string) {}
func (o *openlibrary) Status() Status {
	return Status{SourceID: o.ID(), State: "ready"}
}

func (o *openlibrary) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	u := fmt.Sprintf("%s/search.json?q=%s&limit=%d&fields=key,title,author_name,first_publish_year,cover_i,language,subject,ia,has_fulltext",
		olSearchBase, url.QueryEscape(query), limit)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open library: HTTP %d", resp.StatusCode)
	}

	var sr olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("open library: decode: %w", err)
	}

	out := make([]Result, 0, len(sr.Docs))
	for _, d := range sr.Docs {
		r := Result{
			SourceID:   o.ID(),
			SourceName: o.Name(),
			ID:         d.Key,
			Title:      d.Title,
			Authors:    d.AuthorName,
			CoverURL:   olCoverURL(d.CoverI),
			Language:   firstStr(d.Language),
		}
		if d.FirstPublishYear > 0 {
			r.Year = strconv.Itoa(d.FirstPublishYear)
		}
		if len(d.Subject) > 0 {
			r.Description = strings.Join(subjectPreview(d.Subject), ", ")
		}
		// OL is metadata-only in v1 — no download formats.
		// The cover + description enrichment is the primary value.
		out = append(out, r)
	}
	return out, nil
}

func (o *openlibrary) Download(ctx context.Context, f Format, w io.Writer) error {
	return fmt.Errorf("open library: download not supported — use Gutenberg, Standard Ebooks, or Z-Library for acquisition")
}

// --- Cover enrichment (used by ingest pipeline) ---

// EnrichCover fetches a cover from OL by title+author match. Returns the
// image bytes and a file extension, or nil when no cover is found.
func EnrichCover(ctx context.Context, client *http.Client, title string, authors []string) ([]byte, string, error) {
	if client == nil {
		client = NewHTTPClient()
	}

	// Build a focused query: title + first author
	q := title
	if len(authors) > 0 {
		q += " " + authors[0]
	}
	u := fmt.Sprintf("%s/search.json?q=%s&limit=1&fields=cover_i",
		olSearchBase, url.QueryEscape(q))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("open library: search HTTP %d", resp.StatusCode)
	}

	var sr olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, "", err
	}
	if len(sr.Docs) == 0 || sr.Docs[0].CoverI == 0 {
		return nil, "", nil // no cover found — not an error
	}

	coverURL := olCoverURL(sr.Docs[0].CoverI)
	if coverURL == "" {
		return nil, "", nil
	}

	// Fetch the actual image
	imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
	if err != nil {
		return nil, "", err
	}
	imgReq.Header.Set("User-Agent", UserAgent)

	imgResp, err := client.Do(imgReq)
	if err != nil {
		return nil, "", err
	}
	defer imgResp.Body.Close()

	if imgResp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("open library: cover HTTP %d", imgResp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(imgResp.Body, 5*1024*1024)) // 5MB max
	if err != nil {
		return nil, "", err
	}

	ext := ".jpg"
	ct := imgResp.Header.Get("Content-Type")
	if strings.Contains(ct, "png") {
		ext = ".png"
	}
	return data, ext, nil
}

// --- helpers ---

func olCoverURL(coverI int) string {
	if coverI == 0 {
		return ""
	}
	return fmt.Sprintf("%s/b/id/%d-L.jpg", olCoversBase, coverI)
}

func firstStr(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

// subjectPreview returns at most 5 subjects for description enrichment.
func subjectPreview(subjects []string) []string {
	n := len(subjects)
	if n > 5 {
		n = 5
	}
	return subjects[:n]
}
