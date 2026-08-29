package library

// Cover extraction: pull the cover image out of an EPUB via its OPF
// (meta name="cover" or a manifest item with properties="cover-image",
// falling back to the first image that looks like a cover).

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strings"
)

// epubMeta returns title, authors, year, subjects, and description parsed
// from the package document.
//
// Matching is by local element name only, via a token scan: real-world OPFs
// qualify Dublin Core elements with a dc: prefix or a default xmlns, and
// encoding/xml's struct tags are namespace-sensitive — `xml:"metadata>dc:title"`
// matches nothing on a standards-compliant EPUB (found live: every download
// silently lost its year/subjects/description).
func epubMeta(epubPath string) (string, []string, int, []string, string) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return "", nil, 0, nil, ""
	}
	defer zr.Close()
	opfPath := findOPF(&zr.Reader)
	if opfPath == "" {
		return "", nil, 0, nil, ""
	}
	raw := member(&zr.Reader, opfPath)
	if raw == nil {
		return "", nil, 0, nil, ""
	}

	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	var (
		depth                     []string // local-name stack; metadata lives at depth 2
		inMeta                    int
		cur                       strings.Builder // field name being captured, "" = skip
		buf                       strings.Builder
		title, desc               string
		creators, dates, subjects []string
	)
	capture := func(local string) bool {
		switch local {
		case "title", "creator", "date", "subject", "description":
			return true
		}
		return false
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth = append(depth, t.Name.Local)
			if len(depth) == 2 && t.Name.Local == "metadata" {
				inMeta = len(depth)
			}
			if inMeta > 0 && len(depth) > inMeta && capture(t.Name.Local) {
				cur.Reset()
				cur.WriteString(t.Name.Local)
				buf.Reset()
			}
		case xml.CharData:
			if cur.Len() > 0 {
				buf.Write(t)
			}
		case xml.EndElement:
			if cur.Len() > 0 && t.Name.Local == cur.String() {
				v := strings.TrimSpace(buf.String())
				if v != "" {
					switch cur.String() {
					case "title":
						if title == "" {
							title = v
						}
					case "creator":
						creators = append(creators, v)
					case "date":
						dates = append(dates, v)
					case "subject":
						subjects = append(subjects, v)
					case "description":
						if desc == "" {
							desc = v
						}
					}
				}
				cur.Reset()
			}
			if len(depth) > 0 {
				depth = depth[:len(depth)-1]
			}
			if inMeta > 0 && len(depth) < inMeta {
				inMeta = 0
			}
		}
	}
	year := 0
	if len(dates) > 0 {
		year = ParseYear(dates[0])
	}
	return title, creators, year, subjects, desc
}

// extractCover returns encoded image bytes + extension for the EPUB's cover.
func extractCover(epubPath string) ([]byte, string, bool) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, "", false
	}
	defer zr.Close()

	opfPath := findOPF(&zr.Reader)
	if opfPath != "" {
		raw := member(&zr.Reader, opfPath)
		if raw != nil {
			var pkg struct {
				Meta []struct {
					Name    string `xml:"name,attr"`
					Content string `xml:"content,attr"`
				} `xml:"metadata>meta"`
				Manifest []struct {
					ID         string `xml:"id,attr"`
					Href       string `xml:"href,attr"`
					MediaType  string `xml:"media-type,attr"`
					Properties string `xml:"properties,attr"`
				} `xml:"manifest>item"`
			}
			if xml.Unmarshal(raw, &pkg) == nil {
				coverID := ""
				for _, m := range pkg.Meta {
					if m.Name == "cover" && m.Content != "" {
						coverID = m.Content
						break
					}
				}
				for _, item := range pkg.Manifest {
					isPropsCover := strings.Contains(item.Properties, "cover-image")
					if isPropsCover || (coverID != "" && item.ID == coverID) || looksLikeCoverHref(item.Href) {
						if !strings.HasPrefix(item.MediaType, "image/") {
							continue
						}
						href := path.Join(path.Dir(opfPath), item.Href)
						if img := member(&zr.Reader, href); img != nil {
							return normalizeImage(img), extOf(item.Href), true
						}
					}
				}
			}
		}
	}
	// Last resort: any reasonably sized image named like a cover.
	for _, name := range []string{"cover.jpg", "cover.png", "OEBPS/cover.jpg", "Images/cover.jpg"} {
		if img := member(&zr.Reader, name); img != nil {
			return normalizeImage(img), extOf(name), true
		}
	}
	return nil, "", false
}

func looksLikeCoverHref(href string) bool {
	h := strings.ToLower(href)
	return strings.Contains(h, "cover") && (strings.HasSuffix(h, ".jpg") ||
		strings.HasSuffix(h, ".jpeg") || strings.HasSuffix(h, ".png"))
}

func findOPF(zr *zip.Reader) string {
	type containerRoot struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	raw := member(zr, "META-INF/container.xml")
	if raw == nil {
		return ""
	}
	var c containerRoot
	if xml.Unmarshal(raw, &c) != nil || len(c.Rootfiles) == 0 {
		return ""
	}
	return c.Rootfiles[0].FullPath
}

func member(zr *zip.Reader, name string) []byte {
	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "./") != strings.TrimPrefix(name, "./") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			return nil
		}
		return b
	}
	return nil
}

func extOf(name string) string { return strings.ToLower(path.Ext(name)) }

// normalizeImage re-encodes non-JPEG covers to JPEG so every consumer gets a
// uniform format; PNG stays PNG when smaller than ~200KB to keep quality.
func normalizeImage(img []byte) []byte {
	if len(img) > 0 && img[0] == 0xFF && len(img) > 3 && img[1] == 0xD8 {
		return img // already JPEG
	}
	return img // v1 passes through; OPDS clients handle png/jpeg fine
}
