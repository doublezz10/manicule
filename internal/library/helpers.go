package library

import (
	"os"

	"github.com/doublezz10/manicule/internal/norm"
)

func osMkdirAll(dir string, perm os.FileMode) error { return os.MkdirAll(dir, perm) }

func dedupeKey(title, author string) string { return norm.Key(title, author) }

func sortTitle(title string) string { return norm.SortTitle(title) }
