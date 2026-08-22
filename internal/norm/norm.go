// Package norm provides the shared normalization rules for dedupe keys and
// sort titles — one definition so search, ingest and sources agree.
package norm

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Key normalizes title + first author into a stable dedupe hash.
func Key(title, firstAuthor string) string {
	sum := sha256.Sum256([]byte(Text(title) + "|" + Text(firstAuthor)))
	return hex.EncodeToString(sum[:8])
}

// Text lowercases and strips everything but alphanumerics (unicode-aware),
// collapsing runs of punctuation/space.
func Text(s string) string {
	var b strings.Builder
	lastAlnum := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastAlnum = true
		} else if lastAlnum {
			b.WriteByte(' ')
			lastAlnum = false
		}
	}
	return strings.TrimSpace(b.String())
}

// SortTitle drops leading articles ("The", "A", "An") for shelving order.
func SortTitle(title string) string {
	t := strings.TrimSpace(title)
	lower := strings.ToLower(t)
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, art) && len(t) > len(art) {
			return strings.TrimSpace(t[len(art):])
		}
	}
	return t
}
