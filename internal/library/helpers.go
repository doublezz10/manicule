package library

import (
	"os"
	"strings"

	"github.com/doublezz10/manicule/internal/norm"
)

func osMkdirAll(dir string, perm os.FileMode) error { return os.MkdirAll(dir, perm) }

func dedupeKey(title, author string) string { return norm.Key(title, author) }

func sortTitle(title string) string { return norm.SortTitle(title) }

// AuthorSortKey reorders a display name for shelving: "Pierce Brown" sorts
// as "brown pierce". Names already in "Last, First" form pass through.
// Plain last-token heuristic — honorifics and suffixes ("Jr.") aren't special-cased.
func AuthorSortKey(name string) string {
	name = strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	if name == "" {
		return ""
	}
	if strings.Contains(name, ",") {
		return strings.ToLower(name)
	}
	parts := strings.Fields(name)
	if len(parts) == 1 {
		return strings.ToLower(name)
	}
	last := parts[len(parts)-1]
	rest := strings.Join(parts[:len(parts)-1], " ")
	return strings.ToLower(last + " " + rest)
}
