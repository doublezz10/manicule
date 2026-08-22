package library

import (
	"os"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root
}

func TestAddListSearchDedupe(t *testing.T) {
	s, _ := openTestStore(t)

	id1, err := s.AddBook(
		&Book{Title: "The Hound of the Baskervilles", Authors: []string{"Arthur Conan Doyle"}, Language: "en"},
		&BookFile{Format: "EPUB", Path: "Doyle/Hound/book.epub"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == 0 {
		t.Fatal("no id assigned")
	}

	// FTS search by title fragment.
	hits, err := s.List("hound", "recent", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Book.Title != "The Hound of the Baskervilles" {
		t.Fatalf("FTS search failed: %+v", hits)
	}

	// Author browse.
	byAuthor, err := s.ByAuthor("Arthur Conan Doyle", 0, 10)
	if err != nil || len(byAuthor) != 1 {
		t.Fatalf("ByAuthor: %v %+v", err, byAuthor)
	}
	authors, _ := s.Authors()
	if len(authors) != 1 || authors[0] != "Arthur Conan Doyle" {
		t.Fatalf("Authors(): %v", authors)
	}

	// Dedupe: same normalized title+author → DuplicateError, no second row.
	existing, dupErr := s.AddBook(
		&Book{Title: "the hound of the baskervilles!", Authors: []string{"Arthur Conan Doyle"}},
		&BookFile{Format: "EPUB", Path: "x/y.epub"},
	)
	var _ *DuplicateError
	if _, ok := dupErr.(*DuplicateError); !ok {
		t.Fatalf("expected duplicate error, got %v", dupErr)
	}
	if existing != id1 {
		t.Fatalf("duplicate reported wrong id %d != %d", existing, id1)
	}
	count, _ := s.Count()
	if count != 1 {
		t.Fatalf("count = %d after duplicate add", count)
	}

	// DeleteBook returns relative file paths for trashing.
	paths, err := s.DeleteBook(id1)
	if err != nil || len(paths) != 1 || filepath.Base(paths[0]) != "book.epub" {
		t.Fatalf("DeleteBook paths: %v %v", err, paths)
	}
	count, _ = s.Count()
	if count != 0 {
		t.Fatalf("count after delete = %d", count)
	}
}

func TestIngestorFilesAuthorTitleAndCleans(t *testing.T) {
	s, root := openTestStore(t)

	// A minimal "EPUB" master to ingest.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "some-messy-name.epub")
	master := buildTinyValidEPUB(t)
	if err := os.WriteFile(srcPath, master, 0o644); err != nil {
		t.Fatal(err)
	}

	ing := &Ingestor{Store: s, CleanOnImport: false, ImageMaxWidth: 800}
	meta := &Book{Title: "A Study in Scarlet", Authors: []string{"Arthur Conan Doyle"}}
	bw, err := ing.ImportFile(t.Context(), srcPath, meta)
	if err != nil {
		t.Fatal(err)
	}
	wantRel := filepath.Join("Arthur Conan Doyle", "A Study in Scarlet", "A Study in Scarlet.epub")
	if bw.Files[0].Path != wantRel {
		t.Fatalf("filed at %q, want %q", bw.Files[0].Path, wantRel)
	}
	if _, err := os.Stat(filepath.Join(root, wantRel)); err != nil {
		t.Fatalf("master not copied into library: %v", err)
	}
	// Original untouched on disk.
	st, err := os.Stat(srcPath)
	if err != nil || st.Size() != int64(len(master)) {
		t.Fatalf("source modified or missing: %v", err)
	}
}

func TestUniqueRelCollision(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(first, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := uniqueRel(first)
	if got == first {
		t.Fatal("collision not resolved")
	}
}
