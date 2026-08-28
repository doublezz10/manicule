// Package sources defines the provider abstraction and Tier 1 adapters.
// Every source implements search + link selection + download; the fleet
// subsystem decides which base URL a request goes to.
package sources

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/doublezz10/manicule/internal/norm"
)

// Format is one downloadable rendition of a result.
type Format struct {
	Name string `json:"name"` // "EPUB", "MOBI", "XTCH", ...
	URL  string `json:"url"`
	Size int64  `json:"size,omitempty"` // bytes, when known
}

// Result is one search hit. Grouped by SourceID in the UI — no fake unified ranking.
type Result struct {
	SourceID    string   `json:"source_id"`
	SourceName  string   `json:"source_name"`
	ID          string   `json:"id"` // source-specific stable id
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Language    string   `json:"language,omitempty"`
	Description string   `json:"description,omitempty"`
	Subjects    []string `json:"subjects,omitempty"`
	CoverURL    string   `json:"cover_url,omitempty"`
	Year        string   `json:"year,omitempty"`
	Formats     []Format `json:"formats"`
}

func (r *Result) PrimaryFormat(prefer []string) *Format {
	for _, want := range prefer {
		for i := range r.Formats {
			if strings.EqualFold(r.Formats[i].Name, want) {
				return &r.Formats[i]
			}
		}
	}
	if len(r.Formats) > 0 {
		return &r.Formats[0]
	}
	return nil
}

// Status is what the UI pills show per source.
type Status struct {
	SourceID string `json:"source_id"`
	State    string `json:"state"` // "ready" | "needs-auth" | "down" | "disabled" | "searching" | "error"
	Message  string `json:"message,omitempty"`
}

// Credentials is the user-supplied auth material a source may need.
type Credentials map[string]string

// ProgressFunc reports bytes copied so far and the advertised total.
// total is 0 when the source does not announce a size.
type ProgressFunc func(done, total int64)

// Source is the trait every adapter implements.
type Source interface {
	ID() string
	Name() string
	Tier() int
	Search(ctx context.Context, query string, limit int) ([]Result, error)
	Download(ctx context.Context, f Format, w io.Writer, onProgress ProgressFunc) error
	NeedsAuth() bool
	SetCredentials(Credentials)
	SetBaseURL(base string) // fleet-selected endpoint; empty = default
	Status() Status
}

// HTTPClient shared by all adapters: sane timeout, polite UA, IPv4-only
// dialing. Target machines may run VPN/TUN tools whose IPv6 routes blackhole
// instead of rejecting, which stalls dual-stack dialing for many seconds;
// IPv4-only keeps every source, download, and cover fetch fast and
// predictable (all supported sources are reachable over IPv4).
func NewHTTPClient() *http.Client {
	d := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "tcp4", addr)
			},
			ForceAttemptHTTP2: true,
		},
	}
}

const UserAgent = "manicule/0.1 (+https://github.com/doublezz10/manicule)"

func httpGet(ctx context.Context, client *http.Client, url string, creds *Credentials) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "*/*")
	if creds != nil {
		if email := (*creds)["email"]; email != "" {
			req.SetBasicAuth(email, "")
		} else if u := (*creds)["username"]; u != "" {
			req.SetBasicAuth(u, (*creds)["password"])
		}
	}
	return client.Do(req)
}

// DedupeKey normalizes title+first-author into a stable hash used by the
// library store for skip-and-notify duplicate detection.
func DedupeKey(title, firstAuthor string) string {
	return norm.Key(title, firstAuthor)
}

// SortFormats orders format names by reader preference (EPUB first).
var FormatPreference = []string{"EPUB", "XTCH"}

func PreferFormat(formats []Format) *Format {
	sort.SliceStable(formats, func(i, j int) bool { return formatRank(formats[i].Name) < formatRank(formats[j].Name) })
	if len(formats) == 0 {
		return nil
	}
	return &formats[0]
}

func formatRank(name string) int {
	switch strings.ToUpper(name) {
	case "EPUB":
		return 0
	case "XTCH":
		return 1
	case "KEPUB":
		return 2
	case "AZW3":
		return 3
	case "MOBI":
		return 4
	default:
		return 9
	}
}

var ErrNoResults = fmt.Errorf("no results")

// ErrNeedsAuth tells the UI to surface the credentials flow for a source.
var ErrNeedsAuth = fmt.Errorf("source requires credentials")

// CopyWithProgress streams r into w, invoking onProgress with the cumulative
// count and the advertised total after every chunk (and once up-front so the
// UI can size the bar before the first byte lands).
func CopyWithProgress(w io.Writer, r io.Reader, total int64, onProgress ProgressFunc) error {
	if onProgress == nil {
		_, err := io.Copy(w, r)
		return err
	}
	if total < 0 {
		total = 0
	}
	onProgress(0, total)
	buf := make([]byte, 32*1024)
	var done int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			onProgress(done, total)
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
