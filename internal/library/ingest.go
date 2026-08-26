package library

// Ingestion: file a downloaded/watched book into the library as
// Author/Title, extract its cover, run the cleaning pass when enabled,
// and enforce dedupe with skip-and-notify semantics.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/doublezz10/manicule/internal/clean"
)

type Ingestor struct {
	Store             *Store
	CleanOnImport     bool
	ImageMaxWidth     int
	DeleteSourceAfter bool // watch-folder toggle; manual imports never delete
	FilingMode        string // "author-title" (default), "genre-author-title", "decade-author-title"

	// CoverEnricher is an optional hook called when a book has no cover after
	// EPUB extraction. Receives title + authors, returns image bytes and
	// extension (e.g. ".jpg"), or nil when no cover is available. Wired to
	// Open Library's EnrichCover by the app layer.
	CoverEnricher func(ctx context.Context, title string, authors []string) ([]byte, string, error)
}

// ImportFile copies srcPath into the library and registers it. meta supplies
// title/authors/source metadata (watch-folder imports get metadata from
// filename or EPUB OPF). Returns the stored book.
// calibreAuthor recognizes "Last, First" and plain names from filename
// templates; returns "" when the text doesn't look like a name.
func calibreAuthor(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "/\\") {
		return ""
	}
	if i := strings.Index(s, ","); i > 0 {
		last := strings.TrimSpace(s[:i])
		first := strings.TrimSpace(s[i+1:])
		switch {
		case last != "" && first != "":
			return first + " " + last
		case last != "":
			return last
		default:
			return ""
		}
	}
	return s
}

func (in *Ingestor) ImportFile(ctx context.Context, srcPath string, meta *Book) (*BookWithFiles, error) {
	if _, err := os.Stat(srcPath); err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}

	format := formatOf(srcPath)
	if format == "" {
		return nil, fmt.Errorf("ingest: unsupported file type %q", filepath.Ext(srcPath))
	}
	converted := false

	// Odd-format conversion: MOBI/AZW3 → EPUB via Calibre when present.
	if (format == "MOBI" || format == "AZW3") && in.CleanOnImport {
		if out, ok, err := clean.ConvertToEPUB(ctx, srcPath); err != nil {
			return nil, fmt.Errorf("ingest: conversion: %w", err)
		} else if ok {
			srcPath = out
			format = "EPUB"
			converted = true
		}
	}

	// Fill metadata from the EPUB itself when the caller didn't provide any.
	if meta == nil {
		meta = &Book{}
	}
	if meta.Title == "" || len(meta.Authors) == 0 || meta.Year == 0 {
		if t, a, y, s, d := epubMeta(srcPath); t != "" {
			if meta.Title == "" {
				meta.Title = t
			}
			if len(meta.Authors) == 0 {
				meta.Authors = a
			}
			if meta.Year == 0 && y > 0 {
				meta.Year = y
			}
			if len(meta.Subjects) == 0 && len(s) > 0 {
				meta.Subjects = s
			}
			if meta.Description == "" && d != "" {
				meta.Description = d
			}
		}
	}
	if meta.Title == "" {
		base := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
		meta.Title = strings.TrimSpace(strings.ReplaceAll(base, "_", " "))
		// Calibre's "Title - Last, First" save template leaks the author into
		// the filename; split it back out so cards don't read "… - Brown, Pierce".
		if i := strings.LastIndex(meta.Title, " - "); i > 0 {
			if a := calibreAuthor(meta.Title[i+3:]); a != "" {
				meta.Title = strings.TrimSpace(meta.Title[:i])
				meta.Authors = []string{a}
			}
		}
	}
	if len(meta.Authors) == 0 {
		meta.Authors = []string{"Unknown"}
	}

	relDir := computeFilingDir(in.FilingMode, meta)
	absDir := filepath.Join(in.Store.Root(), relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, err
	}

	fileName := sanitizeFS(meta.Title) + ".epub"
	if format != "EPUB" {
		fileName = sanitizeFS(meta.Title) + "." + strings.ToLower(format)
	}
	relPath := uniqueRel(filepath.Join(relDir, fileName))
	absPath := in.Store.AbsPath(relPath)

	if converted || samePath(srcPath, absPath) {
		if err := os.Rename(srcPath, absPath); err != nil {
			if err := copyFile(srcPath, absPath); err != nil {
				return nil, err
			}
		}
	} else if err := copyFile(srcPath, absPath); err != nil {
		return nil, err
	}

	st, _ := os.Stat(absPath)
	bookFile := &BookFile{
		Format:     format,
		Path:       relPath,
		SizeBytes:  sizeOr(st),
		IsOriginal: true,
	}

	id, err := in.Store.AddBook(meta, bookFile)
	if err != nil {
		os.Remove(absPath) // roll back the copied master on dedupe/error
		var dup *DuplicateError
		if errors.As(err, &dup) {
			return nil, err // caller toasts; nothing was added
		}
		return nil, err
	}

	book, err := in.Store.GetBook(id)
	if err != nil || book == nil {
		return nil, err
	}

	in.extractAndAttachCover(book)

	// If EPUB had no cover, try OL enrichment (when wired).
	if book.Book.CoverPath == "" && in.CoverEnricher != nil {
		if imgData, ext, err := in.CoverEnricher(ctx, book.Book.Title, book.Book.Authors); err == nil && len(imgData) > 0 {
			coverRel := filepath.Join(".manicule", "covers", fmt.Sprintf("%d%s", book.Book.ID, ext))
			if err := os.WriteFile(in.Store.AbsPath(coverRel), imgData, 0o644); err == nil {
				_ = in.Store.UpdateCover(book.Book.ID, coverRel)
				book.Book.CoverPath = coverRel
			}
		}
	}

	// Cleaning pass writes Book.clean.epub alongside the untouched master.
	if in.CleanOnImport && format == "EPUB" {
		cleanName := sanitizeFS(meta.Title) + ".clean.epub"
		cleanRel := filepath.Join(relDir, cleanName)
		rep, err := clean.Clean(absPath, in.Store.AbsPath(cleanRel), clean.Options{ImageMaxWidth: in.ImageMaxWidth})
		if err != nil {
			os.Remove(in.Store.AbsPath(cleanRel))
			fmt.Fprintf(os.Stderr, "ingest: clean failed for %q: %v\n", meta.Title, err)
		} else if cst, err2 := os.Stat(in.Store.AbsPath(cleanRel)); err2 == nil {
			_ = rep
			_ = in.Store.AddFile(&BookFile{
				BookID:     id,
				Format:     "EPUB",
				Path:       cleanRel,
				SizeBytes:  cst.Size(),
				IsOriginal: false,
			})
		}
	}

	// Refresh for cover/derived files already attached above.
	final, err := in.Store.GetBook(id)
	if err != nil {
		return nil, err
	}

	if in.DeleteSourceAfter && !samePath(srcPath, absPath) {
		os.Remove(srcPath)
	}
	return final, nil
}

func (in *Ingestor) extractAndAttachCover(book *BookWithFiles) {
	var master *BookFile
	for i := range book.Files {
		if book.Files[i].IsOriginal && book.Files[i].Format == "EPUB" {
			master = &book.Files[i]
			break
		}
	}
	if master == nil {
		return
	}
	img, ext, ok := extractCover(in.Store.AbsPath(master.Path))
	if !ok {
		return
	}
	relCover := filepath.Join(".manicule", "covers", fmt.Sprintf("%d%s", book.Book.ID, ext))
	if err := os.WriteFile(in.Store.AbsPath(relCover), img, 0o644); err != nil {
		return
	}
	_ = in.Store.UpdateCover(book.Book.ID, relCover)
	book.Book.CoverPath = relCover
}

// --- helpers ---------------------------------------------------------------

var badFSSep = func(r rune) bool { return r == '/' || r == '\\' }

// computeFilingDir builds the directory path based on the active filing mode.
func computeFilingDir(mode string, meta *Book) string {
	author := sanitizeFS(meta.FirstAuthor())
	title := sanitizeFS(meta.Title)
	switch mode {
	case "genre-author-title":
		genre := "Uncategorized"
		if len(meta.Subjects) > 0 {
			genre = sanitizeFS(meta.Subjects[0])
		}
		return filepath.Join(genre, author, title)
	case "decade-author-title":
		decade := "Unknown"
		if meta.Decade != "" {
			decade = sanitizeFS(meta.Decade)
		} else if meta.Year > 0 {
			decade = ComputeDecade(meta.Year)
		}
		return filepath.Join(decade, author, title)
	default: // "author-title"
		return filepath.Join(author, title)
	}
}

func sanitizeFS(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\x00':
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	parts := strings.FieldsFunc(name, badFSSep)
	out := strings.Join(parts, " ")
	for strings.HasSuffix(out, ".") {
		out = strings.TrimSuffix(out, ".")
	}
	if out == "" {
		out = "Unknown"
	}
	return out
}

// uniqueRel appends (2), (3)... when the destination already exists.
func uniqueRel(rel string) string {
	if _, err := os.Stat(rel); os.IsNotExist(err) {
		return rel
	}
	ext := filepath.Ext(rel)
	base := strings.TrimSuffix(rel, ext)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

func formatOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return "EPUB"
	case ".mobi":
		return "MOBI"
	case ".azw3":
		return "AZW3"
	default:
		return ""
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func sizeOr(st os.FileInfo) int64 {
	if st == nil {
		return 0
	}
	return st.Size()
}

func samePath(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}
