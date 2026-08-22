// Package clean implements the e-ink cleaning pipeline: a native Go port of
// bigbag's optimizer recipe. Originals are sacred — cleaning always writes a
// NEW file alongside the master (`Book.clean.epub`), never in place.
package clean

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// Options tunes one cleaning pass.
type Options struct {
	ImageMaxWidth int // px cap; images wider than this are downscaled
}

type Report struct {
	ImagesConverted int      `json:"images_converted"`
	ImagesDropped   int      `json:"images_dropped"`
	FontsStripped   int      `json:"fonts_stripped"`
	RulesRemoved    int      `json:"rules_removed"`
	Warnings        []string `json:"warnings,omitempty"`
}

var posFixed = regexp.MustCompile(`(?i)position\s*:\s*(fixed|absolute)`)

// CleanCSS strips layout CSS that wrecks e-ink rendering:
// floats, flex/grid, fixed/absolute positioning, and @font-face blocks.
func CleanCSS(css string) (string, *Report) {
	rep := &Report{}
	css = removeFontFaces(css, rep)
	css = stripBannedDeclarations(css, rep)
	return css, rep
}

// fontFaceRe finds @font-face blocks with balanced-ish single-level braces.
var fontFaceStart = regexp.MustCompile(`(?i)@font-face\s*\{`)

func removeFontFaces(css string, rep *Report) string {
	for {
		loc := fontFaceStart.FindStringIndex(css)
		if loc == nil {
			return css
		}
		depth := 1
		i := loc[1]
		for i < len(css) && depth > 0 {
			switch css[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			i++
		}
		end := i
		if end < len(css) && css[end] == '}' {
			end++
		}
		css = css[:loc[0]] + css[end:]
		rep.FontsStripped++
	}
}

func stripBannedDeclarations(css string, rep *Report) string {
	var b strings.Builder
	for _, block := range splitRules(css) {
		b.WriteString(cleanRuleText(block, rep))
	}
	return b.String()
}

// splitRules walks CSS text preserving at-rules and selector blocks verbatim
// except for declaration-level surgery inside their bodies.
func splitRules(css string) []string {
	var parts []string
	depth := 0
	start := 0
	inComment := false
	for i := 0; i < len(css); i++ {
		if inComment {
			if i >= 1 && css[i-1] == '*' && css[i] == '/' {
				inComment = false
			}
			continue
		}
		switch css[i] {
		case '/':
			if i+1 < len(css) && css[i+1] == '*' {
				inComment = true
				i++
			}
		case '{', '}':
			parts = append(parts, css[start:i+1])
			start = i + 1
			depth++
		}
	}
	if start < len(css) {
		parts = append(parts, css[start:])
	}
	return parts
}

func cleanRuleText(block string, rep *Report) string {
	open := strings.IndexByte(block, '{')
	close := strings.LastIndexByte(block, '}')
	if open < 0 || close < open {
		return block // selector fragment or at-rule header — keep
	}
	head, body, tail := block[:open+1], block[open+1:close], block[close:]
	var kept []string
	for _, decl := range strings.Split(body, ";") {
		d := strings.TrimSpace(decl)
		if d == "" {
			continue
		}
		if isBannedDeclaration(d) {
			rep.RulesRemoved++
			continue
		}
		kept = append(kept, d)
	}
	return head + strings.Join(kept, ";\n  ") + tail
}

func isBannedDeclaration(d string) bool {
	if posFixed.MatchString(d) {
		return true
	}
	colon := strings.IndexByte(d, ':')
	prop := d
	val := ""
	if colon >= 0 {
		prop, val = d[:colon], d[colon+1:]
	}
	prop = strings.ToLower(strings.TrimSpace(prop))
	switch prop {
	case "float":
		return true
	case "display":
		v := strings.ToLower(val)
		return strings.Contains(v, "flex") || strings.Contains(v, "grid")
	}
	if strings.HasPrefix(prop, "flex") || strings.HasPrefix(prop, "grid") {
		return true
	}
	return false
}

// BaseCSS is appended to every cleaned stylesheet. Deliberately modest:
// it nudges margins and image sizing without fighting the book's own styles.
const BaseCSS = `
/* manicule e-ink base */
body { margin: 4% !important; line-height: 1.45 !important; }
h1 { page-break-before: always !important; }
h1:first-child, h2:first-child, h3:first-child { page-break-before: avoid !important; }
img, svg { max-width: 100% !important; height: auto !important; page-break-inside: avoid !important; }
a { color: inherit !important; text-decoration: none !important; }
`

// SanitizeRelPath rejects zip-slip paths inside EPUB containers.
func SanitizeRelPath(p string) (string, error) {
	clean := path.Clean("/" + p)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("unsafe epub path %q", p)
	}
	return clean, nil
}

// CopyEntry streams one zip member to w.
func CopyEntry(zr *zip.Reader, name string, w io.Writer) error {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = io.Copy(w, rc)
		return err
	}
	return fmt.Errorf("member %q not found", name)
}
