// Library, settings, wizard, and device-facing bindings.
package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doublezz10/manicule/internal/config"
	"github.com/doublezz10/manicule/internal/library"
	"github.com/doublezz10/manicule/internal/trash"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// --- library ----------------------------------------------------------------

func (m *Manicule) ListLibrary(query, sort string, page int) ([]library.BookWithFiles, error) {
	if m.store == nil {
		return nil, nil
	}
	return m.store.List(query, sort, page*20, 50)
}

func (m *Manicule) ListByGenre(genre, sort string, page int) ([]library.BookWithFiles, error) {
	if m.store == nil {
		return nil, nil
	}
	return m.store.ListByGenre(genre, sort, page*20, 50)
}

func (m *Manicule) Genres() ([]string, error) {
	if m.store == nil {
		return nil, nil
	}
	return m.store.Genres()
}

func (m *Manicule) GetBook(id int64) (*library.BookWithFiles, error) {
	if m.store == nil {
		return nil, fmt.Errorf("no library")
	}
	return m.store.GetBook(id)
}

func (m *Manicule) CountBooks() (int, error) {
	if m.store == nil {
		return 0, nil
	}
	return m.store.Count()
}

// DeleteBook removes records and moves files to OS trash — never rm (§5.3).
func (m *Manicule) DeleteBook(id int64) error {
	if m.store == nil {
		return fmt.Errorf("no library")
	}
	paths, err := m.store.DeleteBook(id)
	if err != nil {
		return err
	}
	for _, rel := range paths {
		if _, err := trash.Move(m.store.AbsPath(rel)); err != nil {
			return err
		}
	}
	m.emit("library:changed", nil)
	return nil
}

// RevertClean deletes derived clean files; the master was always untouched.
func (m *Manicule) RevertClean(id int64) error {
	if m.store == nil {
		return fmt.Errorf("no library")
	}
	bw, err := m.store.GetBook(id)
	if err != nil || bw == nil {
		return fmt.Errorf("book not found")
	}
	var kept []library.BookFile
	for i := range bw.Files {
		f := &bw.Files[i]
		if !f.IsOriginal {
			trash.Move(m.store.AbsPath(f.Path))
			m.store.RemoveFile(f.ID)
			continue
		}
		kept = append(kept, *f)
	}
	m.emit("library:changed", nil)
	_ = kept
	return nil
}

func (m *Manicule) ImportFiles() error {
	if m.store == nil {
		return fmt.Errorf("pick a library folder first")
	}
	a := application.Get()
	dlg := a.Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		SetTitle("Import books")
	dlg.AddFilter("E-books", "*.epub;*.mobi;*.azw3")
	files, err := dlg.PromptForMultipleSelection()
	if err != nil || len(files) == 0 {
		return err
	}
	ing := &library.Ingestor{Store: m.store, CleanOnImport: m.settings.CleanOnImport, ImageMaxWidth: m.settings.ImageMaxWidth, FilingMode: m.settings.FilingMode, CoverEnricher: m.coverEnricher}
	go func() {
		imported := 0
		for _, f := range files {
			if _, err := ing.ImportFile(context.Background(), f, nil); err != nil {
				var dup *library.DuplicateError
				if !asDup(err, &dup) {
					continue
				}
			}
			imported++
		}
		m.emit("library:changed", imported)
	}()
	return nil
}

func asDup(err error, target **library.DuplicateError) bool {
	dup, ok := err.(*library.DuplicateError)
	if ok {
		*target = dup
	}
	return ok
}

// PickFolder opens the native directory chooser (wizard + settings).
func (m *Manicule) PickFolder(title string) (string, error) {
	a := application.Get()
	path, err := a.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle(title).
		PromptForSingleSelection()
	return path, err
}

// OpenExternal hands a URL to the OS default browser. The webview blocks
// target=_blank navigation, so every outbound link (Ko-fi, library file
// downloads) routes through this binding instead.
func (m *Manicule) OpenExternal(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("only http(s) URLs can be opened")
	}
	a := application.Get()
	if a == nil || a.Browser == nil {
		return fmt.Errorf("app is not running")
	}
	return a.Browser.OpenURL(u.String())
}

// --- settings / wizard -------------------------------------------------------

func (m *Manicule) GetSettings() *config.Settings { return m.settings }

type SaveSettingsRequest struct {
	LibraryPath         *string                      `json:"library_path,omitempty"`
	SourcesEnabled      map[string]*bool             `json:"sources_enabled,omitempty"`
	Tier2Ack            *bool                        `json:"tier2_acknowledged,omitempty"`
	Credentials         map[string]map[string]string `json:"source_credentials,omitempty"`
	CleanOnImport       *bool                        `json:"clean_on_import,omitempty"`
	ImageMaxWidth       *int                         `json:"image_max_width,omitempty"`
	DeleteAfter         *bool                        `json:"delete_source_after_import,omitempty"`
	WatchEnabled        *bool                        `json:"watch_enabled,omitempty"`
	WatchPath           *string                      `json:"watch_path,omitempty"`
	ServerEnabled       *bool                        `json:"server_enabled,omitempty"`
	ServerPort          *int                         `json:"server_port,omitempty"`
	AuthEnabled         *bool                        `json:"auth_enabled,omitempty"`
	LaunchAtLogin       *bool                        `json:"launch_at_login,omitempty"`
	FleetOverrideSource *string                      `json:"fleet_override_source,omitempty"`
	FleetOverrideURL    *string                      `json:"fleet_override_url,omitempty"`
	FilingMode          *string                      `json:"filing_mode,omitempty"`
	DefaultSource       *string                      `json:"default_source,omitempty"`
	AutoSendDevice      *bool                        `json:"auto_send_device,omitempty"`
}

// SaveSettings applies a partial update and persists it. Heavyweight side
// effects (library move, server restart, watcher restart) happen here too so
// the frontend never has to orchestrate them.
func (m *Manicule) SaveSettings(req SaveSettingsRequest) (*config.Settings, error) {
	s := m.settings

	if req.LibraryPath != nil && *req.LibraryPath != "" && *req.LibraryPath != s.LibraryPath {
		s.LibraryPath = *req.LibraryPath
		m.reopenLibrary()
	}
	for id, v := range req.SourcesEnabled {
		if v != nil {
			s.SourcesEnabled[id] = *v
		}
	}
	if req.Tier2Ack != nil && *req.Tier2Ack {
		s.Tier2Acknowledged = true
	}
	if req.Credentials != nil {
		if s.SourceCredentials == nil {
			s.SourceCredentials = map[string]map[string]string{}
		}
		for id, c := range req.Credentials {
			s.SourceCredentials[id] = c
		}
		m.syncSources()
	}
	if req.CleanOnImport != nil {
		s.CleanOnImport = *req.CleanOnImport
	}
	if req.ImageMaxWidth != nil && *req.ImageMaxWidth > 0 {
		s.ImageMaxWidth = *req.ImageMaxWidth
	}
	if m.downloads != nil {
		m.downloads.SetCleaning(s.CleanOnImport, s.ImageMaxWidth)
	}
	if req.DeleteAfter != nil {
		s.DeleteSourceAfterImport = *req.DeleteAfter
	}
	if req.DefaultSource != nil {
		s.DefaultSource = strings.TrimSpace(*req.DefaultSource)
	}
	watchChanged := false
	if req.WatchEnabled != nil && *req.WatchEnabled != s.WatchEnabled {
		s.WatchEnabled = *req.WatchEnabled
		watchChanged = true
	}
	if req.WatchPath != nil && *req.WatchPath != s.WatchPath {
		s.WatchPath = *req.WatchPath
		watchChanged = true
	}
	if watchChanged {
		m.startWatch()
	}
	if req.ServerEnabled != nil {
		s.ServerEnabled = *req.ServerEnabled
	}
	portChanged := false
	if req.ServerPort != nil && *req.ServerPort > 0 && *req.ServerPort < 65536 && *req.ServerPort != s.ServerPort {
		s.ServerPort = *req.ServerPort
		portChanged = true
	}
	if req.AuthEnabled != nil {
		s.AuthEnabled = *req.AuthEnabled
		if m.opdsSrv != nil {
			m.opdsSrv.UpdateAuth(s.AuthEnabled, s.Pin)
		}
		m.refreshTray()
	}
	if req.LaunchAtLogin != nil {
		s.LaunchAtLogin = *req.LaunchAtLogin
		m.applyLaunchAtLogin(*req.LaunchAtLogin)
	}
	if req.FleetOverrideSource != nil && req.FleetOverrideURL != nil {
		s.FleetOverrides[sanitizeSourceID(*req.FleetOverrideSource)] = strings.TrimSpace(*req.FleetOverrideURL)
		m.fleet.SetOverride(*req.FleetOverrideSource, strings.TrimSpace(*req.FleetOverrideURL))
	}
	if req.FilingMode != nil && *req.FilingMode != "" {
		s.FilingMode = *req.FilingMode
		if m.downloads != nil {
			m.downloads.SetFilingMode(s.FilingMode)
		}
	}
	if req.AutoSendDevice != nil {
		s.AutoSendDevice = *req.AutoSendDevice
	}

	if err := m.cfg.Save(s); err != nil {
		return nil, err
	}
	if portChanged || (req.ServerEnabled != nil) {
		m.restartServer()
	}
	m.emit("settings:changed", s)
	return s, nil
}

// CompleteWizard persists first-run choices and boots the stack end-to-end.
func (m *Manicule) CompleteWizard(libraryPath string, launchAtLogin bool) (*config.Settings, error) {
	s := m.settings
	s.LibraryPath = libraryPath
	s.LaunchAtLogin = launchAtLogin
	s.WizardDone = true
	if launchAtLogin {
		m.applyLaunchAtLogin(true)
	}
	if err := os.MkdirAll(libraryPath, 0o755); err != nil {
		return nil, err
	}
	if err := m.cfg.Save(s); err != nil {
		return nil, err
	}
	if err := m.openLibrary(); err != nil {
		return nil, err
	}
	m.restartServer()
	m.refreshTray()
	m.emit("settings:changed", s)
	return s, nil
}

func (m *Manicule) reopenLibrary() {
	m.stopWatch()
	if m.opdsSrv != nil {
		m.opdsSrv.Stop()
		m.opdsSrv = nil
	}
	if m.store != nil {
		m.store.Close()
		m.store = nil
	}
	if m.settings.LibraryPath == "" {
		return
	}
	if err := m.openLibrary(); err != nil {
		m.emit("app:error", err.Error())
	}
}

func (m *Manicule) startWatch() {
	m.stopWatch()
	if !m.settings.WatchEnabled || m.settings.WatchPath == "" || m.store == nil {
		return
	}
	ing := &library.Ingestor{
		Store:             m.store,
		CleanOnImport:     m.settings.CleanOnImport,
		ImageMaxWidth:     m.settings.ImageMaxWidth,
		DeleteSourceAfter: m.settings.DeleteSourceAfterImport,
		FilingMode:        m.settings.FilingMode,
		CoverEnricher:     m.coverEnricher,
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.watchCancel = cancel
	w := library.NewWatcher(m.settings.WatchPath, 10*time.Second, ing)
	go w.Run(ctx)
}

func (m *Manicule) stopWatch() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
	}
}

func (m *Manicule) applyLaunchAtLogin(enable bool) {
	a := application.Get()
	if a == nil || a.Autostart == nil {
		return
	}
	if enable {
		a.Autostart.Enable()
	} else {
		a.Autostart.Disable()
	}
}

func sanitizeSourceID(id string) string { return filepath.Base(id) }
