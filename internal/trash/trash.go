// Package trash moves files to the OS trash — the library never rm's (§5.3).
// Best-effort per platform; a hidden fallback folder inside the library root
// is the last resort so user data is always recoverable.
package trash

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Move moves path to the OS trash, returning the destination on success.
func Move(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("trash: %w", err)
	}
	if st.IsDir() && !strings.HasSuffix(path, "/") {
		// fine either way
	}
	switch runtime.GOOS {
	case "darwin":
		if dest := moveToDir(path, os.Getenv("HOME")+"/.Trash"); dest != "" {
			return dest, nil
		}
	case "linux":
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			dataHome = filepath.Join(os.Getenv("HOME"), ".local", "share")
		}
		if dest := moveToDir(path, filepath.Join(dataHome, "Trash", "files")); dest != "" {
			return dest, nil
		}
	case "windows":
		home, err := os.UserHomeDir()
		if err == nil {
			if dest := moveToDir(path, filepath.Join(home, ".manicule-trash")); dest != "" {
				return dest, nil
			}
		}
	}
	// Last resort: never rm — keep inside the library root.
	dest := moveToDir(path, ".manicule-trash")
	if dest == "" {
		return "", fmt.Errorf("trash: could not move %q", path)
	}
	return dest, nil
}

func moveToDir(path, dir string) string {
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	base := filepath.Base(path)
	dest := filepath.Join(dir, base)
	for i := 1; ; i++ {
		if _, err := os.Lstat(dest); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(base)
		dest = filepath.Join(dir, strings.TrimSuffix(base, ext)+fmt.Sprintf(" (%d)", i)+ext)
	}
	if err := os.Rename(path, dest); err != nil {
		// Cross-volume rename fails; fall back to copy+delete-original.
		if copyThenRemove(path, dest) {
			return dest
		}
		return ""
	}
	return dest
}

func copyThenRemove(src, dst string) bool {
	in, err := os.Open(src)
	if err != nil {
		return false
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return false
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		os.Remove(dst)
		return false
	}
	out.Close()
	st, err := os.Stat(src)
	if err != nil {
		os.Remove(dst)
		return false
	}
	os.Chmod(dst, st.Mode())
	return os.Remove(src) == nil
}
