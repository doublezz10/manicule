package main

import (
	"embed"
	"fmt"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/doublezz10/manicule/app"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	manicule := app.New()

	appInstance := application.New(application.Options{
		Name:        "manicule",
		Description: "Search → library → OPDS for your e-reader. ☞",
		Services: []application.Service{
			application.NewService(manicule),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		ErrorHandler: func(err error) {
			slog.Error("wails", "err", err)
		},
	})

	var window application.Window
	if appInstance.Window != nil {
		// dock icon for bare-binary runs — a bundled .app carries it via
		// Info.plist instead, this keeps dev launches on-brand
		appInstance.SetIcon(appIcon)
		window = appInstance.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "manicule ☞",
		Width:     1280,
		Height:    820,
		MinWidth:  1000,
		MinHeight: 640,
		URL:       "/",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 28,
		},
	})
	app.SetMainWindowGetter(func() application.Window { return window })
	}

	// Skip tray in server mode (no native window manager).
	if appInstance.Window != nil && appInstance.SystemTray != nil {
		if tray := appInstance.SystemTray.New(); tray != nil {
			if menu := manicule.TrayMenu(); menu != nil {
				tray.SetMenu(menu)
				manicule.AttachTray(tray)
			}
		}
	}

	if err := appInstance.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
