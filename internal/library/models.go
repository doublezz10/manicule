package library

// Book and BookFile are the persisted domain records. They double as the
// JSON contract with the frontend.

import "time"

type Book struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	SortTitle    string    `json:"sort_title"`
	Authors      []string  `json:"authors"`
	Language     string    `json:"language,omitempty"`
	Description  string    `json:"description,omitempty"`
	CoverPath    string    `json:"cover_path,omitempty"` // relative to library root
	SourceID     string    `json:"source_id,omitempty"`
	SourceBookID string    `json:"source_book_id,omitempty"`
	DedupeKey    string    `json:"-"`
	AddedAt      time.Time `json:"added_at"`
}

func (b *Book) FirstAuthor() string {
	if len(b.Authors) > 0 {
		return b.Authors[0]
	}
	return "Unknown"
}

type BookFile struct {
	ID         int64     `json:"id"`
	BookID     int64     `json:"book_id"`
	Format     string    `json:"format"` // EPUB, MOBI, XTCH...
	Path       string    `json:"path"`   // relative to library root
	SizeBytes  int64     `json:"size_bytes"`
	IsOriginal bool      `json:"is_original"` // false = derived (e.g. Book.clean.epub)
	CreatedAt  time.Time `json:"created_at"`
}
