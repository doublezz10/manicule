package clean

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

func white() color.Color { return color.Gray{Y: 0xFF} }

func pathExt(name string) string { return strings.ToLower(filepath.Ext(name)) }

// zipWriter builds the cleaned EPUB: mimetype first & STORED uncompressed
// (OCF requirement), everything else DEFLATED.
type zipWriter struct {
	f     *os.File
	zw    *zip.Writer
	names map[string]bool
	done  int
	err   error
}

func newZipWriter(path string) (*zipWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &zipWriter{f: f, zw: zip.NewWriter(f), names: map[string]bool{}}, nil
}

func (w *zipWriter) has(name string) bool { return w.names[name] }

func (w *zipWriter) addStored(name string, data []byte) error {
	if err := w.add(name, data, zip.Store); err != nil {
		return err
	}
	w.done++
	return nil
}

func (w *zipWriter) addDeflated(name string, data []byte) error {
	if w.names[name] {
		return fmt.Errorf("clean: duplicate member %q", name)
	}
	return w.add(name, data, zip.Deflate)
}

func (w *zipWriter) add(name string, data []byte, method uint16) error {
	hdr := &zip.FileHeader{Name: name, Method: method}
	hdr.SetMode(0o644)
	fw, err := w.zw.CreateHeader(hdr)
	if err != nil {
		w.err = err
		return err
	}
	if _, err := fw.Write(data); err != nil {
		w.err = err
		return err
	}
	w.names[name] = true
	return nil
}

func (w *zipWriter) finalize() error {
	if err := w.zw.Close(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

func (w *zipWriter) abort() {
	w.zw.Close()
	w.f.Close()
	os.Remove(w.f.Name())
}

// Clean transforms srcPath into a cleaned EPUB written to destPath.
// The source file is never modified.
func Clean(srcPath, destPath string, opts Options) (*Report, error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf("clean: read: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("clean: not a valid zip/epub: %w", err)
	}
	if opts.ImageMaxWidth <= 0 {
		opts.ImageMaxWidth = 1440
	}
	rep := &Report{}

	out, err := newZipWriter(destPath)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			out.abort()
		}
	}()

	var renames = map[string]string{} // old member → new member (images)
	dropped := map[string]bool{}      // omitted members (fonts)
	type pendingText struct {
		name   string
		data   []byte
		isCSS  bool
		isMeta bool // OPF / NCX / container.xml
	}
	var texts []pendingText

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name, err := SanitizeRelPath(f.Name)
		if err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}

		switch {
		case name == "mimetype":
			if err := out.addStored(name, raw); err != nil {
				return nil, err
			}

		case isFontFile(name):
			rep.FontsStripped++
			dropped[name] = true

		case isDroppedImage(name):
			rep.ImagesDropped++
			newName := swapExt(name, ".jpg")
			renames[name] = newName // placeholder written after main pass

		case isRasterImage(name):
			jpg, err := convertImage(raw, opts.ImageMaxWidth)
			newName := swapExt(name, ".jpg")
			renames[name] = newName
			if err != nil {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("undecodable image %s blanked", name))
				rep.ImagesDropped++
			} else {
				if err := out.addDeflated(newName, jpg); err != nil {
					return nil, err
				}
				rep.ImagesConverted++
			}

		case strings.EqualFold(name, "META-INF/container.xml"),
			strings.HasSuffix(strings.ToLower(name), ".opf"),
			strings.HasSuffix(strings.ToLower(name), ".ncx"):
			texts = append(texts, pendingText{name: name, data: raw, isMeta: true})

		case isXHTML(name):
			texts = append(texts, pendingText{name: name, data: raw})

		case strings.EqualFold(pathExt(name), ".css"):
			texts = append(texts, pendingText{name: name, data: raw, isCSS: true})

		default:
			if err := out.addDeflated(name, raw); err != nil {
				return nil, err
			}
		}
	}

	// Pass 2: rewrite text members with rename/drop maps applied.
	for _, t := range texts {
		var rewritten []byte
		switch {
		case t.isCSS:
			cleaned, _ := CleanCSS(rewriteRefs(string(t.data), renames))
			rewritten = []byte(cleaned + "\n" + BaseCSS)
		case t.isMeta:
			rewritten = rewriteXMLRefs(t.data, renames, dropped, rep)
		default:
			rewritten = rewriteXHTML(t.data, renames, rep)
		}
		if err := out.addDeflated(t.name, rewritten); err != nil {
			return nil, err
		}
	}

	// Blank placeholder for every dropped/converted image so references stay resolvable.
	for oldName, newName := range renames {
		if !out.has(newName) {
			if err := out.addDeflated(newName, blankJPEG); err != nil {
				return nil, err
			}
		}
		_ = oldName
	}

	if rep.FontsStripped > 0 {
		rep.Warnings = append(rep.Warnings,
			fmt.Sprintf("%d embedded font(s) removed; reader default font applies", rep.FontsStripped))
	}
	if err := out.finalize(); err != nil {
		return nil, err
	}
	success = true
	return rep, nil
}

func isXHTML(name string) bool {
	switch pathExt(name) {
	case ".xhtml", ".html", ".htm":
		return true
	}
	return false
}

func swapExt(name, newExt string) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + newExt
}

// rewriteRefs applies the rename map to CSS url(...) values.
func rewriteRefs(css string, renames map[string]string) string {
	for old, new := range renames {
		css = strings.ReplaceAll(css, urlPrefix+old+")", urlPrefix+new+")")
		css = strings.ReplaceAll(css, urlPrefix+"\""+old+"\")", urlPrefix+"\""+new+"\")")
		css = strings.ReplaceAll(css, urlPrefix+"'"+old+"')", urlPrefix+"'"+new+"')")
	}
	return css
}

const urlPrefix = "url("

// rewriteXMLRefs walks OPF/NCX XML: rewrites href/src through the rename map
// and deletes manifest items whose target was dropped (fonts).
func rewriteXMLRefs(data []byte, renames map[string]string, dropped map[string]bool, rep *Report) []byte {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var buf bytes.Buffer
	buf.WriteString(xml.Header)

	type skipState struct {
		name string
		attr string
		val  string
	}
	var skip *skipState
	depth := 0

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return data // unparseable metadata: pass through unchanged
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if skip != nil {
				continue
			}
			attrs := make([]xml.Attr, len(t.Attr))
			copy(attrs, t.Attr)
			for i := range attrs {
				key := attrs[i].Name.Local
				if key == "href" || key == "src" || key == "source" {
					target := normalizeRel(attrs[i].Value)
					if repl, ok := renames[target]; ok {
						attrs[i].Value = repl
					}
					if dropped[target] && t.Name.Local == "item" {
						skip = &skipState{name: t.Name.Local, val: target}
						depth--
						continue
					}
				}
			}
			if skip != nil {
				continue
			}
			t.Attr = attrs
			emit(&buf, t)
		case xml.EndElement:
			depth--
			if skip != nil {
				if depth < 0 || t.Name.Local == skip.name {
					if t.Name.Local == skip.name && depth >= 0 {
						skip = nil
					} else if depth < 0 {
						skip = nil
						depth = 0
					}
				}
				continue
			}
			buf.WriteByte('<')
			buf.WriteByte('/')
			buf.WriteString(t.Name.Local)
			buf.WriteByte('>')
		case xml.CharData:
			if skip == nil {
				buf.Write(t)
			}
		case xml.ProcInst, xml.Directive:
			if skip == nil {
				// header already emitted; skip original procinsts
			}
		}
	}
	if rep != nil && skip == nil {
		// no-op; hook for future stats
	}
	return buf.Bytes()
}

func emit(buf *bytes.Buffer, se xml.StartElement) {
	buf.WriteByte('<')
	buf.WriteString(se.Name.Local)
	for _, a := range se.Attr {
		buf.WriteByte(' ')
		if a.Name.Space != "" {
			buf.WriteString(a.Name.Space)
			buf.WriteByte(':')
		}
		buf.WriteString(a.Name.Local)
		buf.WriteString(`="`)
		buf.WriteString(xmlEscape(a.Value))
		buf.WriteString(`"`)
	}
	buf.WriteByte('>')
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func normalizeRel(v string) string { return strings.TrimPrefix(v, "./") }

// rewriteXHTML parses HTML tolerantly, applies renames to src/href, strips
// banned inline styles, cleans <style> blocks, drops svg nodes, and renders.
func rewriteXHTML(data []byte, renames map[string]string, rep *Report) []byte {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil || doc == nil {
		// Fallback: plain-text reference rewrite keeps links working.
		s := string(data)
		for old, nw := range renames {
			s = strings.ReplaceAll(s, old, nw)
		}
		return []byte(s)
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tag := n.Data
			switch tag {
			case "svg":
				img := &html.Node{Type: html.ElementNode, Data: "img",
					Attr: []html.Attribute{{Key: "alt", Val: "[figure unavailable]"}}}
				if p := n.Parent; p != nil {
					p.InsertBefore(img, n)
					p.RemoveChild(n)
				}
				return
			case "style":
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						cleaned, r := CleanCSS(c.Data)
						c.Data = cleaned + "\n" + BaseCSS
						rep.RulesRemoved += r.RulesRemoved
					}
				}
			}
			for i := range n.Attr {
				a := &n.Attr[i]
				switch a.Key {
				case "style":
					if a.Val != "" {
						cleaned, r := CleanCSS("x{" + a.Val + "}")
						inner := strings.TrimSpace(cleaned)
						inner = strings.TrimPrefix(inner, "x{")
						inner = strings.TrimSuffix(inner, "}")
						a.Val = inner
						rep.RulesRemoved += r.RulesRemoved
					}
				case "src", "href":
					if repl, ok := renames[normalizeRel(a.Val)]; ok {
						a.Val = repl
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		s := string(data)
		for old, nw := range renames {
			s = strings.ReplaceAll(s, old, nw)
		}
		return []byte(s)
	}
	return buf.Bytes()
}
