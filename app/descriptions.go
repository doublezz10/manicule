package app

// Description backfill: when a search result opens without a blurb, fetch one
// from Open Library's works API on demand. Results are cached per dedupe key
// for the session; failures aren't cached so a flaky network can recover.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/doublezz10/manicule/internal/norm"
	"github.com/doublezz10/manicule/internal/sources"
)

var (
	descMu    sync.Mutex
	descCache = map[string]string{}
)

// WorkDescription returns a short blurb for a title/author pair, backfilled
// from Open Library when the originating catalog didn't carry one.
func (m *Manicule) WorkDescription(title string, authors []string) (string, error) {
	first := ""
	if len(authors) > 0 {
		first = authors[0]
	}
	key := norm.Key(title, first)

	descMu.Lock()
	cached, ok := descCache[key]
	descMu.Unlock()
	if ok {
		return cached, nil
	}

	d, err := fetchOLDescription(title, authors)
	if err != nil {
		return "", err
	}
	descMu.Lock()
	descCache[key] = d
	descMu.Unlock()
	return d, nil
}

func fetchOLDescription(title string, authors []string) (string, error) {
	q := strings.TrimSpace(title)
	if len(authors) > 0 && authors[0] != "" {
		q += " " + authors[0]
	}
	if q == "" {
		return "", fmt.Errorf("nothing to look up")
	}

	client := sources.NewHTTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	find := fmt.Sprintf("https://openlibrary.org/search.json?q=%s&limit=1&fields=key,title",
		url.QueryEscape(q))
	var sr struct {
		Docs []struct {
			Key string `json:"key"` // /works/OL123W
		} `json:"docs"`
	}
	if err := getJSON(ctx, client, find, &sr); err != nil {
		return "", err
	}
	if len(sr.Docs) == 0 || sr.Docs[0].Key == "" {
		return "", fmt.Errorf("no catalog match")
	}

	var work struct {
		Description json.RawMessage `json:"description"`
	}
	if err := getJSON(ctx, client, "https://openlibrary.org"+sr.Docs[0].Key+".json", &work); err != nil {
		return "", err
	}

	// the works API returns description as either a string or {value: "..."}
	var s string
	if json.Unmarshal(work.Description, &s) == nil && s != "" {
		return truncateDesc(s), nil
	}
	var obj struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(work.Description, &obj) == nil && obj.Value != "" {
		return truncateDesc(obj.Value), nil
	}
	return "", fmt.Errorf("no description in catalog")
}

func getJSON(ctx context.Context, client *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", sources.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("open library: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("open library: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func truncateDesc(s string) string {
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace/html artifacts
	if len(s) > 1200 {
		s = s[:1200] + "…"
	}
	return s
}

// BookBlurb returns the description for a library book: stored text first,
// otherwise a one-time Open Library backfill that is then persisted.
func (m *Manicule) BookBlurb(id int64) (string, error) {
	if m.store == nil {
		return "", fmt.Errorf("library is not open")
	}
	bw, err := m.store.GetBook(id)
	if err != nil || bw == nil {
		return "", fmt.Errorf("book not found")
	}
	if bw.Book.Description != "" {
		return bw.Book.Description, nil
	}
	d, err := fetchOLDescription(bw.Book.Title, bw.Book.Authors)
	if err != nil {
		return "", err
	}
	if err := m.store.UpdateDescription(id, d); err != nil {
		slog.Warn("blurb: persist failed", "book", id, "err", err)
	}
	return d, nil
}

// --- download size probe ----------------------------------------------------

var (
	sizeMu    sync.Mutex
	sizeCache = map[string]int64{}
)

// ProbeFileSize HEADs a catalog download URL and returns its Content-Length
// in bytes, for sources that don't advertise sizes in their search payload.
// Cached per URL for the session.
func (m *Manicule) ProbeFileSize(rawURL string) (int64, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return 0, fmt.Errorf("invalid download URL")
	}
	sizeMu.Lock()
	if cached, ok := sizeCache[u.String()]; ok {
		sizeMu.Unlock()
		return cached, nil
	}
	sizeMu.Unlock()

	client := sources.NewHTTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", sources.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("size probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("size probe: HTTP %d", resp.StatusCode)
	}

	var size int64
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		fmt.Sscan(cl, &size)
	}
	if size <= 0 {
		return 0, fmt.Errorf("size probe: no Content-Length")
	}

	sizeMu.Lock()
	sizeCache[u.String()] = size
	sizeMu.Unlock()
	return size, nil
}
