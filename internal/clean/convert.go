package clean

// Odd-format conversion (MOBI/AZW3 → EPUB): detect Calibre's ebook-convert
// binary and shell out to it. When absent we import the original untouched —
// conversion is a bonus, never a requirement.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// FindEbookConvert locates Calibre's ebook-convert on this machine.
func FindEbookConvert() string {
	if p, err := exec.LookPath("ebook-convert"); err == nil {
		return p
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/calibre.app/Contents/MacOS/ebook-convert"}
	case "windows":
		candidates = []string{
			`C:\Program Files\Calibre2\ebook-convert.exe`,
			`C:\Program Files (x86)\Calibre2\ebook-convert.exe`,
		}
	default: // linux + bsd
		candidates = []string{
			"/usr/bin/ebook-convert",
			"/usr/local/bin/ebook-convert",
			"/opt/calibre/ebook-convert",
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// ConvertToEPUB converts srcPath to EPUB via ebook-convert. Returns the
// output path and true when conversion happened; ok=false when no converter.
func ConvertToEPUB(ctx context.Context, srcPath string) (outPath string, ok bool, err error) {
	bin := FindEbookConvert()
	if bin == "" {
		return "", false, nil
	}
	outPath = strings.TrimSuffix(srcPath, filepath.Ext(srcPath)) + ".converted.epub"
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, srcPath, outPath)
	cmd.Env = append(os.Environ(), "CALIBRE_NO_NATIVE_FILEDIALOGS=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(outPath)
		return "", true, wrapCmdErr("ebook-convert", output, err)
	}
	if _, statErr := os.Stat(outPath); statErr != nil {
		return "", true, wrapCmdErr("ebook-convert", nil, statErr)
	}
	return outPath, true, nil
}

func wrapCmdErr(tool string, output []byte, err error) error {
	tail := ""
	if len(output) > 400 {
		output = output[len(output)-400:]
	}
	if len(output) > 0 {
		tail = ": " + strings.TrimSpace(string(output))
	}
	return fmt.Errorf("%s failed%s", tool, tail)
}
