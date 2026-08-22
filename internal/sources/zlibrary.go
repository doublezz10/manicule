package sources

// Z-Library adapter over the eAPI (reverse-engineered REST API).
//
// Tier 2: opt-in only, behind the disclaimer gate. User supplies their own
// email, password, and mirror base URL. The adapter handles session
// management, authenticated search, and EPUB download.
//
// eAPI flow:
//  1. POST {base}/eapi/user/login  (email + password form) → session cookie
//  2. GET  {base}/s/{query}        (session cookie)        → JSON book list
//  3. GET  {base}/dl/{hash}        (session cookie)        → EPUB stream
//
// Sessions are in-memory only; invalidated on credential or mirror change.
// The download manager's 2-attempt retry handles transient session expiry.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

type zlibrary struct {
	client *http.Client
	base   string      // user-supplied mirror, e.g. "https://singlelogin.re"
	creds  Credentials
	mu     sync.Mutex
	session string    // session cookie value, set after login
	lastStatus Status
}

func NewZLibrary(client *http.Client) Source {
	if client == nil {
		client = NewHTTPClient()
	}
	return &zlibrary{client: client}
}

// --- eAPI response types ---

type zlLoginResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type zlSearchResponse struct {
	Books    []zlBook `json:"books"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	Limit    int      `json:"limit"`
}

type zlBook struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	Authors     string  `json:"authors"`   // comma-separated
	Language    string  `json:"language"`
	Extension   string  `json:"extension"`  // "epub", "pdf", etc.
	Hash        string  `json:"hash"`       // download hash
	Cover       string  `json:"cover"`      // cover image path
	Description string  `json:"description"`
	Year        string  `json:"year"`
	Filesize    float64 `json:"filesize"`
}

// --- Source interface ---

func (z *zlibrary) ID() string   { return "z-library" }
func (z *zlibrary) Name() string { return "Z-Library" }
func (z *zlibrary) Tier() int    { return 2 }

func (z *zlibrary) NeedsAuth() bool {
	return z.creds["email"] == "" || z.creds["password"] == ""
}

func (z *zlibrary) SetCredentials(c Credentials) {
	if c == nil {
		c = Credentials{}
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	// Invalidate session if credentials changed
	if c["email"] != z.creds["email"] || c["password"] != z.creds["password"] {
		z.session = ""
	}
	z.creds = c
}

func (z *zlibrary) SetBaseURL(base string) {
	if base == "" {
		return
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	trimmed := strings.TrimRight(base, "/")
	if trimmed != z.base {
		z.session = "" // mirror changed → invalidate session
		z.base = trimmed
	}
}

func (z *zlibrary) Status() Status {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.lastStatus
}

// --- login ---

func (z *zlibrary) login(ctx context.Context) error {
	z.mu.Lock()
	if z.session != "" {
		z.mu.Unlock()
		return nil
	}
	if z.base == "" {
		z.mu.Unlock()
		return fmt.Errorf("z-library: no mirror URL configured — set it in Settings → More sources")
	}
	if z.NeedsAuth() {
		z.mu.Unlock()
		return ErrNeedsAuth
	}
	email := z.creds["email"]
	pass := z.creds["password"]
	z.mu.Unlock()

	form := url.Values{}
	form.Set("email", email)
	form.Set("password", pass)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		z.base+"/eapi/user/login",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", UserAgent)

	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("z-library: login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrNeedsAuth
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("z-library: login HTTP %d", resp.StatusCode)
	}

	// Extract session from cookies (varies by mirror version)
	for _, c := range resp.Cookies() {
		switch c.Name {
		case "remixsid", "remixsid2", "session", "zs":
			z.mu.Lock()
			z.session = c.Value
			z.mu.Unlock()
			break
		}
	}

	// If no cookie, try JSON body
	z.mu.Lock()
	if z.session == "" {
		var lr zlLoginResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&lr); err == nil {
			if !lr.Success {
				z.mu.Unlock()
				return fmt.Errorf("z-library: login failed: %s", lr.Message)
			}
		}
	}
	if z.session == "" {
		z.mu.Unlock()
		return fmt.Errorf("z-library: no session token received — check credentials and mirror URL")
	}
	z.lastStatus = Status{SourceID: z.ID(), State: "ready"}
	z.mu.Unlock()
	return nil
}

// --- authenticated requests ---

func (z *zlibrary) doAuth(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if err := z.login(ctx); err != nil {
		return nil, err
	}
	z.mu.Lock()
	session := z.session
	z.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, method, z.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if session != "" {
		req.Header.Set("Cookie", "remixsid="+session)
	}
	return z.client.Do(req)
}

// invalidateSession clears the session so the next request re-logins.
func (z *zlibrary) invalidateSession() {
	z.mu.Lock()
	z.session = ""
	z.mu.Unlock()
}

// --- Search ---

func (z *zlibrary) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	resp, err := z.doAuth(ctx, http.MethodGet,
		"/s/"+url.PathEscape(query), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		z.invalidateSession()
		return nil, ErrNeedsAuth
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("z-library: search HTTP %d", resp.StatusCode)
	}

	var sr zlSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("z-library: decode search: %w", err)
	}

	out := make([]Result, 0, len(sr.Books))
	for _, b := range sr.Books {
		r := Result{
			SourceID:    z.ID(),
			SourceName:  z.Name(),
			ID:          fmt.Sprintf("zl-%d", b.ID),
			Title:       b.Title,
			Language:    b.Language,
			Description: b.Description,
			Year:        b.Year,
			CoverURL:    z.resolveCover(b.Cover),
		}
		if b.Authors != "" {
			for _, a := range strings.Split(b.Authors, ",") {
				r.Authors = append(r.Authors, strings.TrimSpace(a))
			}
		}
		ext := strings.ToUpper(b.Extension)
		if ext != "" && ext != "PDF" {
			r.Formats = append(r.Formats, Format{
				Name: ext,
				URL:  fmt.Sprintf("/dl/%s", b.Hash),
				Size: int64(b.Filesize),
			})
		}
		if len(r.Formats) > 0 {
			out = append(out, r)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}

	z.mu.Lock()
	z.lastStatus = Status{SourceID: z.ID(), State: "ready"}
	z.mu.Unlock()
	return out, nil
}

// --- Download ---

func (z *zlibrary) Download(ctx context.Context, f Format, w io.Writer) error {
	resp, err := z.doAuth(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		z.invalidateSession()
		return ErrNeedsAuth
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("z-library: download HTTP %d", resp.StatusCode)
	}

	if _, err := io.Copy(w, resp.Body); err != nil {
		z.mu.Lock()
		z.lastStatus = Status{SourceID: z.ID(), State: "error", Message: err.Error()}
		z.mu.Unlock()
		return err
	}
	return nil
}

// --- helpers ---

func (z *zlibrary) resolveCover(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	z.mu.Lock()
	base := z.base
	z.mu.Unlock()
	return base + "/" + strings.TrimPrefix(path, "/")
}
