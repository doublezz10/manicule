package library

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildTinyValidEPUB makes a small but structurally valid EPUB for ingest tests.
func buildTinyValidEPUB(t *testing.T) []byte {
	t.Helper()
	files := []struct{ name, body string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>A Study in Scarlet</dc:title><dc:creator>Arthur Conan Doyle</dc:creator><dc:identifier id="id">test-1</dc:identifier></metadata><manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`},
		{"c1.xhtml", `<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>x</title></head><body><p>Chapter.</p></body></html>`},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, f := range files {
		method := zip.Deflate
		if i == 0 {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
