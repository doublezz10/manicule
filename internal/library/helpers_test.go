package library

import "testing"

func TestAuthorSortKey(t *testing.T) {
	cases := map[string]string{
		"Pierce Brown":           "brown pierce",
		"Robert A. Caro":         "caro robert a.",
		"Brown, Pierce":          "brown, pierce",
		"Gabriel García Márquez": "márquez gabriel garcía",
		"Plato":                  "plato",
		"":                       "",
	}
	for in, want := range cases {
		if got := AuthorSortKey(in); got != want {
			t.Errorf("AuthorSortKey(%q) = %q, want %q", in, got, want)
		}
	}
}
