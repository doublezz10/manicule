// Sync planning: scan the device's SD card, compare it against the manicule
// library, and produce the on-device / missing / orphan views the Devices
// page renders. Device layout follows the official Calibre plugin's
// convention: /<Author>/<Title>.<ext> in the SD root.
package device

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/doublezz10/manicule/internal/norm"
)

// LibBook is the slice of a library record the planner needs (app layer maps
// library.BookWithFiles onto this, preferring the cleaned EPUB for send).
type LibBook struct {
	ID       int64
	Title    string
	Author   string // first author
	Format   string // EPUB, MOBI, ...
	SendPath string // local file to push
	SendSize int64
}

// DeviceFile is one e-book file found on the device.
type DeviceFile struct {
	Path string
	Size int64
}

// Match pairs a library book with its device presence (DevicePath empty =
// not on the device yet).
type Match struct {
	BookID     int64  `json:"book_id"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Format     string `json:"format"`
	DevicePath string `json:"device_path,omitempty"`
	DeviceSize int64  `json:"device_size,omitempty"`
	RemotePath string `json:"remote_path"` // where manicule files it on the device
	SendPath   string `json:"-"`
	SendSize   int64  `json:"-"`
}

// Plan is the Devices page model.
type Plan struct {
	OnDevice []Match      `json:"on_device"`
	Missing  []Match      `json:"missing"`
	Orphan   []DeviceFile `json:"orphan"` // book files on device, not in library
}

// bookExts are the formats the planner treats as books (everything else on
// the card — fonts, covers, system folders — is ignored).
var bookExts = map[string]bool{
	".epub": true, ".mobi": true, ".azw3": true, ".kepub": true, ".fb2": true,
}

// WalkBooks recursively lists the SD card and returns every e-book file.
func WalkBooks(ctx context.Context, c *Client) ([]DeviceFile, error) {
	var out []DeviceFile
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if depth > 8 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := c.ListDir(ctx, dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			p := path.Join(dir, e.Name)
			if e.IsDir {
				if err := walk(p, depth+1); err != nil {
					return err
				}
				continue
			}
			if bookExts[strings.ToLower(path.Ext(e.Name))] {
				out = append(out, DeviceFile{Path: p, Size: e.Size})
			}
			if len(out) > 20000 { // absurd card guard
				return nil
			}
		}
		return nil
	}
	if err := walk("/", 0); err != nil {
		return nil, err
	}
	return out, nil
}

// sortArticleRe matches Calibre's title-sort suffix ("Way of Kings, The").
var sortArticleRe = regexp.MustCompile(`(?i)^(.+), (the|a|an)$`)

// canonTitle rotates a trailing sort article to the front so the library's
// "The Man In The High Castle" and the card's "Man In The High Castle, The"
// compare equal.
func canonTitle(t string) string {
	if m := sortArticleRe.FindStringSubmatch(strings.TrimSpace(t)); m != nil {
		base := strings.TrimSpace(m[1])
		if base != "" {
			return m[2] + " " + base
		}
	}
	return t
}

// titleKey is the normalized comparison form of a title: sort-article
// rotation first, then norm.Text.
func titleKey(t string) string { return norm.Text(canonTitle(t)) }

// splitDevicePath breaks "/Author/Title.ext" into its match parts. Calibre
// save templates embed the author in the filename ("Title - Author.ext") and
// write "Last, First" author folders — both get normalized, and the
// filename's author (when present) is trusted over the folder's, since
// plugin folders can be mangled ("K., Dick, Philip").
func splitDevicePath(p string) (title, author string) {
	dir, base := path.Split(strings.TrimPrefix(p, "/"))
	base = strings.TrimSuffix(base, path.Ext(base))
	author = flipAuthor(strings.Trim(dir, "/"))
	if i := strings.LastIndex(base, " - "); i > 0 {
		author = flipAuthor(strings.TrimSpace(base[i+3:]))
		base = strings.TrimSpace(base[:i])
	}
	return base, author
}

// flipAuthor converts Calibre's "Last, First" author folders — which the
// official plugin writes to the card — into "First Last", matching how the
// library stores author names.
func flipAuthor(a string) string {
	if i := strings.IndexByte(a, ','); i > 0 {
		last := strings.TrimSpace(a[:i])
		rest := strings.TrimSpace(a[i+1:])
		if rest != "" {
			return rest + " " + last
		}
		return last
	}
	return a
}

// PlanBooks compares the library against the device's files. Matching runs in
// three passes — normalized author+title (with Calibre "Last, First" folders
// flipped), then title-only for files loose in the card root, then
// unique-title rescue for books whose device folder is mangled — so anything
// manicule or Calibre pushed always matches, and each device file counts at
// most once.
func PlanBooks(books []LibBook, files []DeviceFile) Plan {
	byTitle := map[string][]int{} // norm(title) → books indexes, for the flat pass
	for i, b := range books {
		byTitle[titleKey(b.Title)] = append(byTitle[titleKey(b.Title)], i)
	}

	fileOf := make([]int, len(books)) // books index → matched files index, -1 none
	for i := range fileOf {
		fileOf[i] = -1
	}
	usedFile := make([]bool, len(files))

	// pass 1: exact planned path or normalized author+title key
	for i, b := range books {
		wantKey := titleKey(b.Title) + "|" + norm.Text(b.Author)
		wantPath := RemotePathFor(b.Author, b.Title, b.Format)
		for j, f := range files {
			if usedFile[j] {
				continue
			}
			title, author := splitDevicePath(f.Path)
			if f.Path == wantPath ||
				(author != "" && wantKey == titleKey(title)+"|"+norm.Text(flipAuthor(author))) {
				fileOf[i], usedFile[j] = j, true
				break
			}
		}
	}

	// pass 2: files loose in the card root match on title alone
	for j, f := range files {
		if usedFile[j] {
			continue
		}
		title, author := splitDevicePath(f.Path)
		if author != "" {
			continue
		}
		for _, bi := range byTitle[titleKey(title)] {
			if fileOf[bi] == -1 {
				fileOf[bi], usedFile[j] = j, true
				break
			}
		}
	}

	// pass 3: a still-missing book claims an orphan file with the exact same
	// title — but only when that title is unambiguous on the card, so
	// same-title-different-author pairs stay unmatched rather than wrong
	for i, b := range books {
		if fileOf[i] != -1 {
			continue
		}
		want := titleKey(b.Title)
		if want == "" {
			continue
		}
		cand, hits := -1, 0
		for j, f := range files {
			if usedFile[j] {
				continue
			}
			if title, _ := splitDevicePath(f.Path); titleKey(title) == want {
				cand, hits = j, hits+1
			}
		}
		if hits == 1 {
			fileOf[i], usedFile[cand] = cand, true
		}
	}

	// pass 4: truncation rescue. Calibre save templates cap filename length,
	// so the library title can be a prefix of the card's full title ("Bug
	// Music How Insects Gave Us Rhythm and" vs "…Rhythm and Noise"). Only
	// fires on long titles with a matching author and exactly one candidate —
	// short titles stay exact-match territory ("Dune" must never claim
	// "Dune Messiah").
	for i, b := range books {
		if fileOf[i] != -1 {
			continue
		}
		want, wantAuthor := titleKey(b.Title), norm.Text(b.Author)
		if wantAuthor == "" {
			continue
		}
		cand, hits := -1, 0
		for j, f := range files {
			if usedFile[j] {
				continue
			}
			title, author := splitDevicePath(f.Path)
			got := titleKey(title)
			if got == want || norm.Text(author) != wantAuthor {
				continue
			}
			shorter := got
			if len(want) < len(got) {
				shorter = want
			}
			if len(shorter) < 16 {
				continue
			}
			if strings.HasPrefix(got, want) || strings.HasPrefix(want, got) {
				cand, hits = j, hits+1
			}
		}
		if hits == 1 {
			fileOf[i], usedFile[cand] = cand, true
		}
	}

	var plan = Plan{OnDevice: []Match{}, Missing: []Match{}, Orphan: []DeviceFile{}}
	for i, b := range books {
		m := Match{
			BookID: b.ID, Title: b.Title, Author: b.Author, Format: b.Format,
			RemotePath: RemotePathFor(b.Author, b.Title, b.Format),
			SendPath:   b.SendPath, SendSize: b.SendSize,
		}
		if j := fileOf[i]; j >= 0 {
			m.DevicePath = files[j].Path
			m.DeviceSize = files[j].Size
			plan.OnDevice = append(plan.OnDevice, m)
		} else {
			plan.Missing = append(plan.Missing, m)
		}
	}
	for j, f := range files {
		if !usedFile[j] {
			plan.Orphan = append(plan.Orphan, f)
		}
	}
	return plan
}

// RemotePathFor is where manicule files a book on the device:
// /<Author>/<Title>.<ext> in the card root (Calibre-plugin convention).
func RemotePathFor(author, title, format string) string {
	ext := strings.ToLower(strings.TrimSpace(format))
	if ext == "" {
		ext = "epub"
	}
	return "/" + SanitizeName(author) + "/" + SanitizeName(title) + "." + ext
}

// SanitizeName makes one path component FAT/SD-safe: forbidden characters
// become spaces, control characters are dropped, and the result is trimmed
// and capped (long names overflow the device's file listing UI).
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 32 || strings.ContainsRune(`\/:*?"<>|`, r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.Trim(out, ". ")
	runes := []rune(out)
	if len(runes) > 96 {
		out = strings.TrimSpace(string(runes[:96]))
	}
	if out == "" {
		out = "Unknown"
	}
	return out
}

// EnsureDirs creates the author folder for a remote path, tolerating
// already-exists errors (the firmware offers no exists check).
func EnsureDirs(ctx context.Context, c *Client, remotePath string) {
	dir := path.Dir(remotePath)
	if dir == "" || dir == "/" || dir == "." {
		return
	}
	parent, name := path.Split(strings.TrimPrefix(dir, "/"))
	_ = c.Mkdir(ctx, "/"+strings.TrimSuffix(parent, "/"), strings.TrimSuffix(name, "/"))
}
