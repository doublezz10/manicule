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
	"sort"
	"strconv"
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
	year           INTEGER NOT NULL DEFAULT 0,
	subjects_json  TEXT NOT NULL DEFAULT '[]',
	decade         TEXT NOT NULL DEFAULT '',
	author_sort    TEXT NOT NULL DEFAULT '',
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
	title, authors, description, subjects,
	content='books', content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS books_ai AFTER INSERT ON books BEGIN
	INSERT INTO books_fts(rowid, title, authors, description, subjects)
	VALUES (new.id, new.title, new.authors_json, new.description, new.subjects_json);
END;
CREATE TRIGGER IF NOT EXISTS books_ad AFTER DELETE ON books BEGIN
	INSERT INTO books_fts(books_fts, rowid, title, authors, description, subjects)
	VALUES ('delete', old.id, old.title, old.authors_json, old.description, old.subjects_json);
END;
CREATE TRIGGER IF NOT EXISTS books_au AFTER UPDATE OF title, authors_json, description, subjects_json ON books BEGIN
	INSERT INTO books_fts(books_fts, rowid, title, authors, description, subjects)
	VALUES ('delete', old.id, old.title, old.authors_json, old.description, old.subjects_json);
	INSERT INTO books_fts(rowid, title, authors, description, subjects)
	VALUES (new.id, new.title, new.authors_json, new.description, new.subjects_json);
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
	// Migration: add year/subjects/decade columns for databases created before this feature.
	for _, col := range []string{
		`ALTER TABLE books ADD COLUMN year INTEGER DEFAULT 0`,
		`ALTER TABLE books ADD COLUMN subjects_json TEXT DEFAULT '[]'`,
		`ALTER TABLE books ADD COLUMN decade TEXT DEFAULT ''`,
		`ALTER TABLE books ADD COLUMN author_sort TEXT DEFAULT ''`,
	} {
		db.Exec(col) // errors expected if column already exists
	}
	// Backfill author_sort for rows created before the column existed.
	backfillAuthorSort(db)
	s := &Store{db: db, rootDir: rootDir}
	s.backfillEpubMeta()
	return s, nil
}

// backfillEpubMeta re-reads EPUB metadata for books imported before the
// namespace-agnostic OPF parser — year/subjects/description were silently
// empty even when the files carried them. Fill-in only: fields the user or a
// richer source already populated are left alone.
func (s *Store) backfillEpubMeta() {
	rows, err := s.db.Query(`SELECT b.id, f.path FROM books b
		JOIN files f ON f.book_id = b.id AND f.is_original = 1
		WHERE b.year = 0 OR b.subjects_json = '' OR b.subjects_json = '[]' OR b.description = ''`)
	if err != nil {
		return
	}
	type job struct {
		id   int64
		path string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.path) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		_, _, y, subs, d := epubMeta(s.AbsPath(j.path))
		if y == 0 && len(subs) == 0 && d == "" {
			continue
		}
		var year int
		var subjectsJSON, description string
		if s.db.QueryRow(`SELECT year, subjects_json, description FROM books WHERE id = ?`, j.id).
			Scan(&year, &subjectsJSON, &description) != nil {
			continue
		}
		if year == 0 {
			year = y
		}
		if (subjectsJSON == "" || subjectsJSON == "[]") && len(subs) > 0 {
			if b, err := json.Marshal(subs); err == nil {
				subjectsJSON = string(b)
			}
		}
		if description == "" {
			description = d
		}
		decade := ""
		if year > 0 {
			decade = fmt.Sprintf("%d0s", year/10*10)
		}
		s.db.Exec(`UPDATE books SET year = ?, subjects_json = ?, description = ?, decade = ? WHERE id = ?`,
			year, subjectsJSON, description, decade, j.id)
	}
}

// backfillAuthorSort computes the last-name-first sort key for any book that
// doesn't have one yet (imports from before the column existed).
func backfillAuthorSort(db *sql.DB) {
	rows, err := db.Query(`SELECT id, authors_json FROM books WHERE author_sort = ''`)
	if err != nil {
		return
	}
	type pending struct {
		id  int64
		key string
	}
	var updates []pending
	for rows.Next() {
		var id int64
		var authorsJSON string
		if err := rows.Scan(&id, &authorsJSON); err != nil {
			continue
		}
		var authors []string
		if json.Unmarshal([]byte(authorsJSON), &authors) != nil || len(authors) == 0 {
			continue
		}
		updates = append(updates, pending{id: id, key: AuthorSortKey(authors[0])})
	}
	rows.Close()
	for _, u := range updates {
		if u.key != "" {
			db.Exec(`UPDATE books SET author_sort = ? WHERE id = ?`, u.key, u.id)
		}
	}
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
	subjectsJSON, _ := json.Marshal(b.Subjects)
	if b.Decade == "" && b.Year > 0 {
		b.Decade = ComputeDecade(b.Year)
	}
	authorSort := ""
	if len(b.Authors) > 0 {
		authorSort = AuthorSortKey(b.Authors[0])
	}

	res, err := s.db.Exec(`INSERT INTO books
		(title, sort_title, authors_json, language, description, year, subjects_json, decade, author_sort, cover_path, source_id, source_book_id, dedupe_key, added_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.Title, b.SortTitle, string(authorsJSON), b.Language, b.Description,
		b.Year, string(subjectsJSON), b.Decade, authorSort,
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
		order = ` ORDER BY author_sort COLLATE NOCASE ASC, sort_title COLLATE NOCASE ASC`
	case "recent", "":
		order = ` ORDER BY added_at DESC`
	case "year":
		order = ` ORDER BY year DESC, sort_title COLLATE NOCASE ASC`
	case "decade":
		order = ` ORDER BY decade DESC, sort_title COLLATE NOCASE ASC`
	case "genre":
		order = ` ORDER BY subjects_json ASC, sort_title COLLATE NOCASE ASC`
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
	rows, err := s.db.Query(`SELECT DISTINCT je.value AS author
		FROM books, json_each(books.authors_json) je`)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// shelf order: by surname, not display order
	sort.Slice(out, func(i, j int) bool {
		ki, kj := AuthorSortKey(out[i]), AuthorSortKey(out[j])
		if ki == kj {
			return out[i] < out[j]
		}
		return ki < kj
	})
	return out, nil
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

// Genres returns distinct first-subject values (genre chips for the UI).
func (s *Store) Genres() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT je.value FROM books, json_each(books.subjects_json) je
		WHERE je.value != '' ORDER BY je.value COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListByGenre filters books by having a matching value in subjects_json.
func (s *Store) ListByGenre(genre, sort string, offset, limit int) ([]BookWithFiles, error) {
	base := `SELECT DISTINCT ` + bookCols + ` FROM books
		WHERE EXISTS (SELECT 1 FROM json_each(books.subjects_json) je WHERE je.value = ?)`
	order := ` ORDER BY sort_title COLLATE NOCASE ASC`
	switch sort {
	case "year":
		order = ` ORDER BY year DESC, sort_title COLLATE NOCASE ASC`
	case "decade":
		order = ` ORDER BY decade DESC, sort_title COLLATE NOCASE ASC`
	case "recent":
		order = ` ORDER BY added_at DESC`
	}
	rows, err := s.db.Query(base+order+limitClause(offset, limit), genre)
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

// UpdateDescription stores a backfilled blurb (FTS trigger keeps the index
// in sync).
func (s *Store) UpdateDescription(bookID int64, desc string) error {
	_, err := s.db.Exec(`UPDATE books SET description = ? WHERE id = ?`, desc, bookID)
	return err
}

// --- plumbing ------------------------------------------------------------

const bookCols = `books.id, books.title, books.sort_title, books.authors_json, books.language, books.description,
	books.year, books.subjects_json, books.decade,
	books.cover_path, books.source_id, books.source_book_id, books.dedupe_key, books.added_at`

func (s *Store) scanBook(row *sql.Row) (*Book, error) {
	var b Book
	var authorsJSON, subjectsJSON, added string
	if err := row.Scan(&b.ID, &b.Title, &b.SortTitle, &authorsJSON, &b.Language, &b.Description,
		&b.Year, &subjectsJSON, &b.Decade,
		&b.CoverPath, &b.SourceID, &b.SourceBookID, &b.DedupeKey, &added); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(authorsJSON), &b.Authors)
	json.Unmarshal([]byte(subjectsJSON), &b.Subjects)
	b.AddedAt, _ = time.Parse(time.RFC3339, added)
	return &b, nil
}

func (s *Store) collect(rows *sql.Rows) ([]BookWithFiles, error) {
	var out []BookWithFiles
	for rows.Next() {
		var b Book
		var authorsJSON, subjectsJSON, added string
		if err := rows.Scan(&b.ID, &b.Title, &b.SortTitle, &authorsJSON, &b.Language, &b.Description,
			&b.Year, &subjectsJSON, &b.Decade,
			&b.CoverPath, &b.SourceID, &b.SourceBookID, &b.DedupeKey, &added); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(authorsJSON), &b.Authors)
		json.Unmarshal([]byte(subjectsJSON), &b.Subjects)
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

// ComputeDecade returns a decade string like "1920s" from a year.
func ComputeDecade(year int) string {
	if year <= 0 {
		return ""
	}
	decade := (year / 10) * 10
	return strconv.Itoa(decade) + "s"
}

// ParseYear extracts a year from a string like "2024" or "2024-01-15".
func ParseYear(s string) int {
	if s == "" {
		return 0
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil || y < 0 || y > 3000 {
		return 0
	}
	return y
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
