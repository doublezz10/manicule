// Package library is the SQLite-backed store: books, files, FTS5 search,
// duplicate detection. The database and covers live under
// <LibraryRoot>/.manicule/ so the whole library stays one portable folder.
package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DuplicateError signals skip-and-notify dedupe hits; ExistingID lets the UI
// offer per-book Replace / Keep-both later.
type DuplicateError struct {
	Title      string
	ExistingID int64
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("already in library: %q (book #%d)", e.Title, e.ExistingID)
}

type Store struct {
	db      *sql.DB
	rootDir string // absolute library root; all stored paths are relative to it
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS books (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	title          TEXT NOT NULL,
	sort_title     TEXT NOT NULL DEFAULT '',
	authors_json   TEXT NOT NULL DEFAULT '[]',
	language       TEXT NOT NULL DEFAULT '',
	description    TEXT NOT NULL DEFAULT '',
	cover_path     TEXT NOT NULL DEFAULT '',
	source_id      TEXT NOT NULL DEFAULT '',
	source_book_id TEXT NOT NULL DEFAULT '',
	dedupe_key     TEXT NOT NULL,
	added_at       TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_books_dedupe ON books(dedupe_key);

CREATE TABLE IF NOT EXISTS book_files (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	format      TEXT NOT NULL,
	path        TEXT NOT NULL,
	size_bytes  INTEGER NOT NULL DEFAULT 0,
	is_original INTEGER NOT NULL DEFAULT 1,
	created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_book_files_book ON book_files(book_id);

CREATE VIRTUAL TABLE IF NOT EXISTS books_fts USING fts5(
	title, authors, description,
	content='books', content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS books_ai AFTER INSERT ON books BEGIN
	INSERT INTO books_fts(rowid, title, authors, description)
	VALUES (new.id, new.title, new.authors_json, new.description);
END;
CREATE TRIGGER IF NOT EXISTS books_ad AFTER DELETE ON books BEGIN
	INSERT INTO books_fts(books_fts, rowid, title, authors, description)
	VALUES ('delete', old.id, old.title, old.authors_json, old.description);
END;
CREATE TRIGGER IF NOT EXISTS books_au AFTER UPDATE OF title, authors_json, description ON books BEGIN
	INSERT INTO books_fts(books_fts, rowid, title, authors, description)
	VALUES ('delete', old.id, old.title, old.authors_json, old.description);
	INSERT INTO books_fts(rowid, title, authors, description)
	VALUES (new.id, new.title, new.authors_json, new.description);
END;
`

func Open(rootDir string) (*Store, error) {
	meta := filepath.Join(rootDir, ".manicule")
	if err := ensureDir(meta); err != nil {
		return nil, err
	}
	if err := ensureDir(filepath.Join(meta, "covers")); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(meta, "library.db"))
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is pure Go; a single writer keeps things simple and safe.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("library: schema: %w", err)
	}
	return &Store{db: db, rootDir: rootDir}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Root() string { return s.rootDir }

// AbsPath resolves a stored relative path to an absolute one.
func (s *Store) AbsPath(rel string) string { return filepath.Join(s.rootDir, rel) }

// AddBook inserts a book + its file. Dedupe on normalized title+author hash:
// callers surface DuplicateError as a toast, never a modal.
func (s *Store) AddBook(b *Book, f *BookFile) (int64, error) {
	b.DedupeKey = dedupeKey(b.Title, b.FirstAuthor())
	if b.AddedAt.IsZero() {
		b.AddedAt = time.Now()
	}
	if b.SortTitle == "" {
		b.SortTitle = sortTitle(b.Title)
	}
	authorsJSON, _ := json.Marshal(b.Authors)

	res, err := s.db.Exec(`INSERT INTO books
		(title, sort_title, authors_json, language, description, cover_path, source_id, source_book_id, dedupe_key, added_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		b.Title, b.SortTitle, string(authorsJSON), b.Language, b.Description,
		b.CoverPath, b.SourceID, b.SourceBookID, b.DedupeKey, b.AddedAt.Format(time.RFC3339))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "constraint failed") {
			var existing int64
			qErr := s.db.QueryRow(`SELECT id FROM books WHERE dedupe_key = ?`, b.DedupeKey).Scan(&existing)
			if qErr == nil {
				return existing, &DuplicateError{Title: b.Title, ExistingID: existing}
			}
		}
		return 0, fmt.Errorf("library: add book: %w", err)
	}
	id, _ := res.LastInsertId()
	b.ID = id
	f.BookID = id
	if err := s.AddFile(f); err != nil {
		return id, err
	}
	return id, nil
}

// ReplaceFile swaps a derived/original file for a book (used by re-clean).
func (s *Store) AddFile(f *BookFile) error {
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	isOrig := 0
	if f.IsOriginal {
		isOrig = 1
	}
	res, err := s.db.Exec(`INSERT INTO book_files (book_id, format, path, size_bytes, is_original, created_at)
		VALUES (?,?,?,?,?,?)`,
		f.BookID, f.Format, f.Path, f.SizeBytes, isOrig, f.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("library: add file: %w", err)
	}
	f.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) RemoveFile(fileID int64) (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT path FROM book_files WHERE id = ?`, fileID).Scan(&path)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`DELETE FROM book_files WHERE id = ?`, fileID)
	return path, err
}

// GetBook hydrates one book with its files attached.
func (s *Store) GetBook(id int64) (*BookWithFiles, error) {
	b, err := s.scanBook(s.db.QueryRow(`SELECT `+bookCols+` FROM books WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files, err := s.FilesFor(b.ID)
	if err != nil {
		return nil, err
	}
	return &BookWithFiles{Book: *b, Files: files}, nil
}

type BookWithFiles struct {
	Book  Book       `json:"book"`
	Files []BookFile `json:"files"`
}

// List returns books. Empty query → everything sorted by `sort`;
// non-empty query → FTS5 match against title/authors/description.
func (s *Store) List(query, sort string, offset, limit int) ([]BookWithFiles, error) {
	if strings.TrimSpace(query) != "" && !isFTSQuery(query) {
		query = ftsQuote(query)
	}
	base := `SELECT DISTINCT ` + bookCols + ` FROM books`
	order := ` ORDER BY added_at DESC`
	switch sort {
	case "title":
		order = ` ORDER BY sort_title COLLATE NOCASE ASC`
	case "author":
		order = ` ORDER BY json_extract(authors_json, '$[0]') COLLATE NOCASE ASC, sort_title COLLATE NOCASE ASC`
	case "recent":
		order = ` ORDER BY added_at DESC`
	}
	var args []any
	if strings.TrimSpace(query) != "" {
		base += ` JOIN books_fts ON books_fts.rowid = books.id WHERE books_fts MATCH ?`
		args = append(args, query)
	}
	order += limitClause(offset, limit)
	rows, err := s.db.Query(base+order, args...)
	if err != nil {
		return nil, fmt.Errorf("library: list: %w", err)
	}
	defer rows.Close()
	return s.collect(rows)
}

func (s *Store) Newest(offset, limit int) ([]BookWithFiles, error) {
	rows, err := s.db.Query(`SELECT ` + bookCols + ` FROM books ORDER BY added_at DESC, id DESC ` + limitClause(offset, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.collect(rows)
}

func (s *Store) Authors() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT json_extract(value, '$') AS author
		FROM books, json_each(books.authors_json) ORDER BY author COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ByAuthor(author string, offset, limit int) ([]BookWithFiles, error) {
	rows, err := s.db.Query(`SELECT DISTINCT `+bookCols+` FROM books
		WHERE EXISTS (SELECT 1 FROM json_each(books.authors_json) je WHERE je.value = ?)
		ORDER BY sort_title COLLATE NOCASE ASC `+limitClause(offset, limit), author)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.collect(rows)
}

func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&n)
	return n, err
}

// DeleteBook removes DB records and returns every relative path that should
// be moved to OS trash by the caller — the store itself never rm's.
func (s *Store) DeleteBook(id int64) ([]string, error) {
	files, err := s.FilesFor(id)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM books WHERE id = ?`, id); err != nil {
		tx.Rollback()
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM book_files WHERE book_id = ?`, id); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files)+1)
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths, nil
}

func (s *Store) FilesFor(bookID int64) ([]BookFile, error) {
	rows, err := s.db.Query(`SELECT id, book_id, format, path, size_bytes, is_original, created_at
		FROM book_files WHERE book_id = ? ORDER BY is_original DESC, format ASC`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BookFile
	for rows.Next() {
		var f BookFile
		var isOrig int
		var created string
		if err := rows.Scan(&f.ID, &f.BookID, &f.Format, &f.Path, &f.SizeBytes, &isOrig, &created); err != nil {
			return nil, err
		}
		f.IsOriginal = isOrig == 1
		f.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, f)
	}
	return out, rows.Err()
}

// UpdateCover sets cover_path after extraction.
func (s *Store) UpdateCover(bookID int64, relPath string) error {
	_, err := s.db.Exec(`UPDATE books SET cover_path = ? WHERE id = ?`, relPath, bookID)
	return err
}

// --- plumbing ------------------------------------------------------------

const bookCols = `id, title, sort_title, authors_json, language, description, cover_path, source_id, source_book_id, dedupe_key, added_at`

func (s *Store) scanBook(row *sql.Row) (*Book, error) {
	var b Book
	var authorsJSON, added string
	if err := row.Scan(&b.ID, &b.Title, &b.SortTitle, &authorsJSON, &b.Language, &b.Description,
		&b.CoverPath, &b.SourceID, &b.SourceBookID, &b.DedupeKey, &added); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(authorsJSON), &b.Authors)
	b.AddedAt, _ = time.Parse(time.RFC3339, added)
	return &b, nil
}

func (s *Store) collect(rows *sql.Rows) ([]BookWithFiles, error) {
	var out []BookWithFiles
	for rows.Next() {
		var b Book
		var authorsJSON, added string
		if err := rows.Scan(&b.ID, &b.Title, &b.SortTitle, &authorsJSON, &b.Language, &b.Description,
			&b.CoverPath, &b.SourceID, &b.SourceBookID, &b.DedupeKey, &added); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(authorsJSON), &b.Authors)
		b.AddedAt, _ = time.Parse(time.RFC3339, added)
		out = append(out, BookWithFiles{Book: b})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		files, err := s.FilesFor(out[i].Book.ID)
		if err != nil {
			return nil, err
		}
		out[i].Files = files
	}
	return out, nil
}

func limitClause(offset, limit int) string {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
}

// isFTSQuery heuristically passes through queries already using FTS operators.
func isFTSQuery(q string) bool {
	return strings.ContainsAny(q, `"*`) || strings.Contains(q, " AND ") || strings.Contains(q, " OR ")
}

// ftsQuote makes free text a prefix-friendly FTS query.
func ftsQuote(q string) string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '"' || r == '(' || r == ')'
	})
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, `"`+strings.ReplaceAll(f, `"`, ``)+`"*`)
	}
	return strings.Join(parts, " ")
}

func ensureDir(dir string) error {
	if err := osMkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("library: mkdir %s: %w", dir, err)
	}
	return nil
}
