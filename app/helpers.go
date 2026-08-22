package app

import (
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func setClipboard(text string) {
	a := application.Get()
	if a != nil {
		a.Clipboard.SetText(text)
	}
}
