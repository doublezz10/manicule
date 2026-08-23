package main

import (
	"context"
	"fmt"
	"time"
	"github.com/doublezz10/manicule/internal/sources"
)

func main() {
	client := sources.NewHTTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test Gutendex
	fmt.Println("=== GUTENDEX ===")
	g := sources.NewGutendex(client)
	gres, err := g.Search(ctx, "pride and prejudice", 3)
	if err != nil {
		fmt.Println("  ERROR:", err)
	} else {
		for i, r := range gres {
			fmt.Printf("  %d. %s by %v [%s]\n", i+1, r.Title, r.Authors, r.Year)
			for _, f := range r.Formats {
				fmt.Printf("     → %s\n", f.Name)
			}
		}
	}

	// Test Open Library
	fmt.Println("\n=== OPEN LIBRARY ===")
	ol := sources.NewOpenLibrary(client)
	olres, err := ol.Search(ctx, "pride and prejudice", 3)
	if err != nil {
		fmt.Println("  ERROR:", err)
	} else {
		for i, r := range olres {
			fmt.Printf("  %d. %s by %v [%s] subjects=%v\n", i+1, r.Title, r.Authors, r.Year, r.Subjects[:min(3, len(r.Subjects))])
			fmt.Printf("     cover: %s\n", r.CoverURL[:min(60, len(r.CoverURL))])
		}
	}

	// Test Standard Ebooks (needs auth)
	fmt.Println("\n=== STANDARD EBOOKS ===")
	se := sources.NewStandardEbooks(client)
	fmt.Printf("  NeedsAuth: %v\n", se.NeedsAuth())

	// Test Z-Library (needs auth)
	fmt.Println("\n=== Z-LIBRARY ===")
	zl := sources.NewZLibrary(client)
	fmt.Printf("  NeedsAuth: %v (no credentials set)\n", zl.NeedsAuth())
	zl.SetCredentials(sources.Credentials{"email": "test@test.com", "password": "pass"})
	zl.SetBaseURL("https://singlelogin.re")
	fmt.Printf("  NeedsAuth after creds: %v\n", zl.NeedsAuth())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
