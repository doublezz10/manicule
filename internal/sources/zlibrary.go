package sources

// Z-Library adapter over the eAPI (the z-library app API).
//
// VERIFIED LIVE 2026-08-29 against the writer's own account:
//   - login:  POST /eapi/user/login {email,password} → JSON
//     {success:1, user:{id, remix_userkey, ...}} — auth material comes in
//     the BODY, not as a session cookie. Auth = remix_userid + remix_userkey
//     cookies on every later request.
//   - search: POST /eapi/book/search {message, limit} → {books:[...]} with
//     id/hash/title/author/year/extension/filesize/cover per entry.
//   - download: GET /eapi/book/{id}/{hash}/file → {file:{downloadLink}} →
//     GET the link → file bytes.
//   - Mirrors rotate constantly. z-library.bz (the public site) fronts
//     everything with a JS anti-bot challenge AND no longer serves /eapi —
//     so the adapter solves the challenge when offered (it is plain
//     client-side arithmetic: server sets __js_p_=<code>,<age>,…; client
//     sets __jhash_=get_jhash(code) + __jua_=encoded UA and retries) and
//     rotates through known-good mirrors when the configured one 404s the
//     eAPI. libb.la is the official "most reliable" mirror and works clean.
//   - z-lib's edge gates on User-Agent: requests use a browser UA.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// zlibUserAgent must look like a browser: the mirror's edge serves the JS
// challenge unconditionally to non-browser UAs.
const zlibUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15"

// zlFallbackMirrors are tried, in order, after the user-configured mirror.
// libb.la is Z-Library's own "most reliable user mirror" (announced 2026-07).
var zlFallbackMirrors = []string{"https://libb.la", "https://1lib.sk", "https://singlelogin.re"}

type zlibrary struct {
	client *http.Client
	mu     sync.Mutex
	base   string // user-configured mirror (may be empty → default)
	creds  Credentials

	mirror     string            // mirror currently in use (rotated on failure)
	tried      map[string]bool   // mirrors that failed this session
	cookies    map[string]string // auth + solved-challenge cookies for mirror
	lastStatus Status
}

func NewZLibrary(client *http.Client) Source {
	if client == nil {
		client = NewHTTPClient()
	}
	return &zlibrary{client: client}
}

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
	// invalidate everything if credentials or mirror changed
	if c["email"] != z.creds["email"] || c["password"] != z.creds["password"] || c["base_url"] != z.creds["base_url"] {
		z.cookies = nil
		z.mirror = ""
		z.tried = nil
	}
	z.creds = c
	z.base = strings.TrimRight(strings.TrimSpace(c["base_url"]), "/")
}

func (z *zlibrary) SetBaseURL(base string) {
	if base == "" {
		return
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	trimmed := strings.TrimRight(base, "/")
	if trimmed != z.base {
		z.cookies = nil
		z.mirror = ""
		z.tried = nil
		z.base = trimmed
	}
}

func (z *zlibrary) Status() Status {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.lastStatus
}

// setStatus records the adapter state for the UI.
func (z *zlibrary) setStatus(state, message string) {
	z.mu.Lock()
	z.lastStatus = Status{SourceID: z.ID(), State: state, Message: message}
	z.mu.Unlock()
}

// --- anti-bot challenge (the "__js_p_" page) ---------------------------------

// zlJhash is get_jhash verbatim from the challenge page. JS semantics: all
// values stay below 2^25 so int64 arithmetic matches int32 exactly.
func zlJhash(b int64) int64 {
	x := int64(123456789)
	var k int64
	for i := int64(0); i < 1677696; i++ {
		x = (((x + b) ^ (x + (x % 3) + (x % 17) + b)) ^ i) % 16776960
		if x%117 == 0 {
			k = (k + 1) % 1111
		}
	}
	return k
}

// zlFixedEncodeURIComponent is the challenge page's fixedEncodeURIComponent.
func zlFixedEncodeURIComponent(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// solveChallenge inspects a response for a fresh __js_p_ cookie; when found
// it computes and stores the answer cookies. Returns true when the request
// must be retried.
func (z *zlibrary) solveChallenge(resp *http.Response) bool {
	for _, ck := range resp.Cookies() {
		if ck.Name != "__js_p_" {
			continue
		}
		parts := strings.Split(ck.Value, ",")
		if len(parts) < 2 {
			return false
		}
		code, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			return false
		}
		z.mu.Lock()
		if z.cookies == nil {
			z.cookies = map[string]string{}
		}
		z.cookies["__jhash_"] = strconv.FormatInt(zlJhash(code), 10)
		z.cookies["__jua_"] = zlFixedEncodeURIComponent(zlibUserAgent)
		z.mu.Unlock()
		return true
	}
	return false
}

// --- HTTP plumbing ------------------------------------------------------------

// zlDo performs one request against the active mirror, solving challenges
// and following the anti-bot's 307 bounce. Auth rejections clear the session
// once so the next attempt re-logins. The response body is returned whole.
func (z *zlibrary) zlDo(ctx context.Context, method, path string, form url.Values) ([]byte, int, error) {
	relogged := false
	for attempt := 0; attempt < 4; attempt++ {
		if err := z.ensureLogin(ctx); err != nil {
			return nil, 0, err
		}
		resp, err := z.request(ctx, method, path, form)
		if err != nil {
			return nil, 0, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()

		switch {
		case z.solveChallenge(resp):
			time.Sleep(1100 * time.Millisecond) // the challenge JS waits ~1s before reloading
		case resp.StatusCode == 307 || resp.StatusCode == 302:
			// the anti-bot's bounce: same path, fresh __hash_ cookie in the jar
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			z.mu.Lock()
			hadKey := z.cookies["remix_userkey"] != ""
			z.cookies["remix_userkey"] = ""
			z.mu.Unlock()
			if hadKey && !relogged {
				relogged = true // session expired — re-login and retry
				continue
			}
			return body, resp.StatusCode, ErrNeedsAuth
		default:
			return body, resp.StatusCode, nil
		}
	}
	return nil, 0, fmt.Errorf("z-library: too many retries")
}

// request sends one HTTP round trip with the current mirror + cookie set.
// Every response cookie is folded back into the jar (anti-bot, session).
func (z *zlibrary) request(ctx context.Context, method, path string, form url.Values) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, z.currentMirror()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", zlibUserAgent)
	req.Header.Set("Accept", "application/json, text/html, */*")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if ck := z.cookieHeader(); ck != "" {
		req.Header.Set("Cookie", ck)
	}
	resp, err := z.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("z-library: %w", err)
	}
	z.mu.Lock()
	if z.cookies == nil {
		z.cookies = map[string]string{}
	}
	for _, c := range resp.Cookies() {
		z.cookies[c.Name] = c.Value
	}
	z.mu.Unlock()
	return resp, nil
}

func (z *zlibrary) currentMirror() string {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.mirror != "" {
		return z.mirror
	}
	if z.base != "" {
		return z.base
	}
	return zlFallbackMirrors[0]
}

func (z *zlibrary) cookieHeader() string {
	z.mu.Lock()
	defer z.mu.Unlock()
	parts := make([]string, 0, len(z.cookies))
	for k, v := range z.cookies {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

// rotateMirror moves to the next untried candidate (user's mirror first,
// then the fallback list). Mirrors can fail in any way — DiamWall redirect
// loops, dead eAPI, DNS — so rotation tracks what has been tried rather
// than assuming an order. Returns false when all are exhausted.
func (z *zlibrary) rotateMirror() bool {
	z.mu.Lock()
	defer z.mu.Unlock()
	order := zlFallbackMirrors
	if z.base != "" {
		order = append([]string{z.base}, zlFallbackMirrors...)
	}
	if z.tried == nil {
		z.tried = map[string]bool{}
	}
	if z.mirror != "" {
		z.tried[z.mirror] = true
	}
	for _, m := range order {
		if !z.tried[m] {
			from := z.mirror
			z.mirror = m
			z.cookies = map[string]string{}
			slog.Info("z-library: rotating mirror", "from", from, "to", m)
			return true
		}
	}
	return false
}

// --- login ---------------------------------------------------------------------

type zlLoginResponse struct {
	Success int `json:"success"`
	User    struct {
		ID           int64  `json:"id"`
		Email        string `json:"email"`
		Name         string `json:"name"`
		RemixUserkey string `json:"remix_userkey"`
	} `json:"user"`
	Error string `json:"error"`
}

// ensureLogin performs the eAPI login when the session is cold, rotating
// mirrors when the configured one cannot serve the API.
func (z *zlibrary) ensureLogin(ctx context.Context) error {
	z.mu.Lock()
	if z.cookies["remix_userkey"] != "" {
		z.mu.Unlock()
		return nil
	}
	if z.NeedsAuth() {
		z.mu.Unlock()
		return ErrNeedsAuth
	}
	z.mu.Unlock()

	form := url.Values{"email": {z.creds["email"]}, "password": {z.creds["password"]}}
	for tries := 0; tries < len(zlFallbackMirrors)+2; tries++ {
		resp, err := z.request(ctx, http.MethodPost, "/eapi/user/login", form)
		if err != nil {
			// transport failure — redirect-loop walls (DiamWall), dead DNS,
			// TLS resets: this mirror is hostile, move on
			if z.rotateMirror() {
				continue
			}
			return err
		}
		if z.solveChallenge(resp) {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			time.Sleep(1100 * time.Millisecond) // the challenge JS waits ~1s before reloading
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		// the anti-bot's own 307 bounce (same path, __hash_ now in the jar)
		if loc := resp.Header.Get("Location"); loc != "" && (resp.StatusCode == 307 || resp.StatusCode == 302) {
			continue
		}

		var lr zlLoginResponse
		if jsonErr := json.Unmarshal(body, &lr); jsonErr == nil && lr.Success == 1 && lr.User.RemixUserkey != "" {
			z.mu.Lock()
			if z.cookies == nil {
				z.cookies = map[string]string{}
			}
			z.cookies["remix_userid"] = strconv.FormatInt(lr.User.ID, 10)
			z.cookies["remix_userkey"] = lr.User.RemixUserkey
			z.mu.Unlock()
			z.setStatus("ready", "")
			slog.Info("z-library: logged in", "mirror", z.currentMirror(), "user", lr.User.Name)
			return nil
		}
		// wrong password → surface immediately; anything else → try next mirror
		msg := lr.Error
		if msg == "" {
			msg = strings.TrimSpace(string(body))
			if len(msg) > 120 {
				msg = msg[:120]
			}
		}
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "password") || strings.Contains(lower, "email") || strings.Contains(lower, "captcha") {
			z.setStatus("error", msg)
			return fmt.Errorf("z-library: %s", msg)
		}
		if !z.rotateMirror() {
			z.setStatus("error", msg)
			return fmt.Errorf("z-library: login failed on every mirror: %s", msg)
		}
	}
	z.setStatus("error", "login did not complete")
	return fmt.Errorf("z-library: login did not complete")
}

// --- Source API -------------------------------------------------------------------

type zlBook struct {
	ID        int64   `json:"id"`
	Hash      string  `json:"hash"`
	Title     string  `json:"title"`
	Author    string  `json:"author"`
	Year      int     `json:"year"`
	Extension string  `json:"extension"`
	Filesize  float64 `json:"filesize"`
	Cover     string  `json:"cover"`
	Language  string  `json:"language"`
	Descr     string  `json:"description"`
}

func (z *zlibrary) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	form := url.Values{"message": {query}}
	if limit > 0 {
		form.Set("limit", strconv.Itoa(limit))
	}
	body, code, err := z.zlDo(ctx, http.MethodPost, "/eapi/book/search", form)
	if err != nil {
		if err == ErrNeedsAuth {
			return nil, ErrNeedsAuth
		}
		z.setStatus("error", err.Error())
		return nil, err
	}
	if code != http.StatusOK {
		z.setStatus("error", fmt.Sprintf("search HTTP %d", code))
		return nil, fmt.Errorf("z-library: search HTTP %d", code)
	}
	var sr struct {
		Books []zlBook `json:"books"`
	}
	if err := json.Unmarshal(body, &sr); err != nil {
		z.setStatus("error", "unexpected search response")
		return nil, fmt.Errorf("z-library: decode search: %w", err)
	}
	out := make([]Result, 0, len(sr.Books))
	for _, b := range sr.Books {
		ext := strings.ToUpper(strings.TrimSpace(b.Extension))
		if ext == "" || ext == "PDF" {
			continue // not useful for e-reader ingestion
		}
		r := Result{
			SourceID:    z.ID(),
			SourceName:  z.Name(),
			ID:          fmt.Sprintf("zl-%d-%s", b.ID, b.Hash),
			Title:       b.Title,
			Authors:     splitAuthors(b.Author),
			Language:    b.Language,
			Description: b.Descr,
			Year:        yearString(b.Year),
			CoverURL:    z.resolveCover(b.Cover),
			// one file per eAPI entry; Download resolves it to a CDN link
			Formats: []Format{{
				Name: ext,
				URL:  fmt.Sprintf("/eapi/book/%d/%s/file", b.ID, b.Hash),
				Size: int64(b.Filesize),
			}},
		}
		out = append(out, r)
	}
	z.setStatus("ready", "")
	return out, nil
}

// Download resolves the file-info endpoint to a temporary CDN link, then
// streams the file with progress.
func (z *zlibrary) Download(ctx context.Context, f Format, w io.Writer, onProgress ProgressFunc) error {
	body, code, err := z.zlDo(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("z-library: file info HTTP %d", code)
	}
	var fr struct {
		File struct {
			DownloadLink string `json:"downloadLink"`
		} `json:"file"`
	}
	if jsonErr := json.Unmarshal(body, &fr); jsonErr != nil || fr.File.DownloadLink == "" {
		return fmt.Errorf("z-library: no download link (HTTP %d)", code)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fr.File.DownloadLink, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", zlibUserAgent)
	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("z-library: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("z-library: download HTTP %d", resp.StatusCode)
	}
	return CopyWithProgress(w, resp.Body, resp.ContentLength, onProgress)
}

func (z *zlibrary) resolveCover(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return z.currentMirror() + "/" + strings.TrimPrefix(path, "/")
}

func splitAuthors(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func yearString(y int) string {
	if y <= 0 {
		return ""
	}
	return strconv.Itoa(y)
}
