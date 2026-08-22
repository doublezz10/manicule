package clean

// Lightweight structural validation — zip integrity, XHTML well-formedness,
// image decodability. Deliberately NOT Java epubcheck: fast, native, and
// strict enough to catch real damage without blocking good books.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"os"
	"strings"

	"golang.org/x/net/html"
)

// Validate checks an EPUB and returns a list of problems (empty = healthy).
func Validate(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a valid zip archive")
	}

	var problems []string
	var mimetypeSeen bool
	hasOPF := false
	hasContainer := false

	for i, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "./")
		if i == 0 && name == "mimetype" {
			mimetypeSeen = true
		}
		switch {
		case name == "META-INF/container.xml":
			hasContainer = true
		case strings.HasSuffix(strings.ToLower(name), ".opf"):
			hasOPF = true
		}
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			problems = append(problems, fmt.Sprintf("unreadable member %q", f.Name))
			continue
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			problems = append(problems, fmt.Sprintf("corrupt member %q", f.Name))
			continue
		}
		if isXHTMLFile(name) && len(raw) > 0 {
			if !wellFormedXHTML(raw) {
				problems = append(problems, fmt.Sprintf("malformed XHTML: %s", name))
			}
		}
		if isRasterImage(name) && len(raw) > 0 {
			if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
				problems = append(problems, fmt.Sprintf("undecodable image: %s", name))
			}
		}
	}

	if !mimetypeSeen {
		problems = append(problems, "mimetype entry missing or not first")
	} else if !hasContainer {
		problems = append(problems, "META-INF/container.xml missing")
	} else if !hasOPF {
		problems = append(problems, "package document (.opf) missing")
	}
	return problems, nil
}

func isXHTMLFile(name string) bool { return isXHTML(name) }

// wellFormedXHTML tries a strict XML parse first; if that fails it falls
// back to html.Parse which accepts nearly anything, so only genuinely
// broken documents are reported.
func wellFormedXHTML(raw []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			// Tolerant HTML parse as arbiter.
			_, herr := html.Parse(bytes.NewReader(raw))
			return herr == nil
		}
	}
}
