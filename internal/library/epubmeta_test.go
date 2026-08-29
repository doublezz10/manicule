package library

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// buildMiniEpub writes a minimal EPUB whose OPF declares a default xmlns and
// dc:-prefixed Dublin Core elements — the standards-compliant shape that the
// old struct-tag parser silently matched nothing on.
func buildMiniEpub(t *testing.T, dir string) string {
	t.Helper()
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>
    <dc:title>Jane Eyre: An Autobiography</dc:title>
    <dc:creator>Charlotte Brontë</dc:creator>
    <dc:subject>Fiction</dc:subject>
    <dc:subject>Gothic novel</dc:subject>
    <dc:date>2012-09-03</dc:date>
    <dc:description>Jane Eyre is a novel published in 1847.</dc:description>
  </metadata>
</package>`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`,
		"OEBPS/content.opf":      opf,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(data))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "mini.epub")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEpubMetaNamespacedOPF(t *testing.T) {
	p := buildMiniEpub(t, t.TempDir())
	title, authors, year, subjects, desc := epubMeta(p)
	if title != "Jane Eyre: An Autobiography" {
		t.Fatalf("title = %q", title)
	}
	if len(authors) != 1 || authors[0] != "Charlotte Brontë" {
		t.Fatalf("authors = %v", authors)
	}
	if year != 2012 {
		t.Fatalf("year = %d, want 2012", year)
	}
	if len(subjects) != 2 || subjects[0] != "Fiction" || subjects[1] != "Gothic novel" {
		t.Fatalf("subjects = %v", subjects)
	}
	if desc == "" {
		t.Fatal("description not extracted")
	}
}

// EPUB2 flavor: prefixed elements with no default xmlns must parse the same.
func TestEpubMetaPrefixedOnlyOPF(t *testing.T) {
	dir := t.TempDir()
	opf := `<?xml version="1.0"?>
<package xmlns:dc="http://purl.org/dc/elements/1.1/">
  <metadata>
    <dc:title>Moby Dick</dc:title>
    <dc:creator>Herman Melville</dc:creator>
    <dc:date>1851</dc:date>
  </metadata>
</package>`
	p := filepath.Join(dir, "m.epub")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("content.opf")
	w.Write([]byte(opf))
	w2, _ := zw.Create("META-INF/container.xml")
	w2.Write([]byte(`<?xml version="1.0"?><container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`))
	zw.Close()
	os.WriteFile(p, buf.Bytes(), 0o644)

	title, authors, year, _, _ := epubMeta(p)
	if title != "Moby Dick" || len(authors) != 1 || authors[0] != "Herman Melville" || year != 1851 {
		t.Fatalf("title=%q authors=%v year=%d", title, authors, year)
	}
}
