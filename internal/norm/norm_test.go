package norm

import "testing"

func TestKeyStable(t *testing.T) {
	a := Key("The Adventures of Sherlock Holmes", "Arthur Conan Doyle")
	b := Key("  the ADVENTURES of   sherlock, holmes! ", "arthur conan doyle")
	if a != b {
		t.Fatalf("normalized keys differ:\n%q\n%q", a, b)
	}
	c := Key("A Different Book", "Some Author")
	if a == c {
		t.Fatal("different books share a key")
	}
}

func TestSortTitle(t *testing.T) {
	cases := map[string]string{
		"The Hound of the Baskervilles": "Hound of the Baskervilles",
		"A Study in Scarlet":            "Study in Scarlet",
		"An Experiment":                 "Experiment",
		"Emma":                          "Emma",
	}
	for in, want := range cases {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
