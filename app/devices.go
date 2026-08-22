// Devices page + tray: OPDS server control, QR/PIN display, zero-typing
// provisioning file generation, update check.
package app

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/doublezz10/manicule/internal/config"
	"github.com/doublezz10/manicule/internal/opds"
)

func (m *Manicule) ServerStatus() ServerStatus {
	st := ServerStatus{
		Port:        m.settings.ServerPort,
		URL:         fmt.Sprintf("http://localhost:%d/opds", m.settings.ServerPort),
		LANURL:      opds.LANURL(m.settings.ServerPort),
		PIN:         m.settings.Pin,
		AuthEnabled: m.settings.AuthEnabled,
	}
	if m.opdsSrv != nil {
		st.Running = m.opdsSrv.Running()
	}
	return st
}

// RestartServer applies current settings to the running OPDS server.
func (m *Manicule) RestartServer() error { return m.restartServer() }

func (m *Manicule) restartServer() error {
	if m.opdsSrv != nil {
		m.opdsSrv.Stop()
		m.opdsSrv = nil
	}
	if !m.settings.ServerEnabled || m.store == nil {
		m.refreshTray()
		return nil
	}
	srv := opds.New(m.store, m.settings.ServerPort, m.settings.AuthEnabled, m.settings.Pin)
	if err := srv.Start(); err != nil {
		m.emit("app:error", err.Error())
		return err
	}
	m.opdsSrv = srv
	m.refreshTray()
	m.emit("server:status", m.ServerStatus())
	return nil
}

// RegeneratePin issues a fresh 4-digit PIN and applies it live.
func (m *Manicule) RegeneratePin() (*config.Settings, error) {
	m.settings.Pin = config.GeneratePIN()
	if m.opdsSrv != nil {
		m.opdsSrv.UpdateAuth(m.settings.AuthEnabled, m.settings.Pin)
	}
	if err := m.cfg.Save(m.settings); err != nil {
		return nil, err
	}
	m.refreshTray()
	m.emit("server:status", m.ServerStatus())
	return m.settings, nil
}

// SaveProvisioningFile writes /.crosspoint/opds.json content anywhere the
// user picks (their SD card), enabling the zero-typing device setup path.
func (m *Manicule) SaveProvisioningFile() (string, error) {
	a := application.Get()
	dest, err := a.Dialog.SaveFile().
		SetMessage("Save this as /.crosspoint/opds.json on the device SD card").
		SetFilename("opds.json").
		PromptForSingleSelection()
	if err != nil || dest == "" {
		return "", err
	}
	status := m.ServerStatus()
	data, err := opds.ProvisionJSON(status.LANURL, opds.Username, status.PIN)
	if err != nil {
		return "", err
	}
	if err := writeFile(dest, data); err != nil {
		return "", err
	}
	return dest, nil
}

// GetProvisioningPreview returns the JSON that would land on the SD card.
func (m *Manicule) GetProvisioningPreview() (string, error) {
	status := m.ServerStatus()
	data, err := opds.ProvisionJSON(status.LANURL, opds.Username, status.PIN)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// CheckForUpdates hits GitHub releases for a newer tag; v1 ships a manual
// check link rather than auto-update (risk table §8).
type UpdateInfo struct {
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	Update  bool   `json:"update"`
	URL     string `json:"url"`
}

const currentVersion = "0.1.0-dev"

func (m *Manicule) CheckForUpdates() (*UpdateInfo, error) {
	info := &UpdateInfo{Current: currentVersion, URL: "https://github.com/doublezz10/manicule/releases"}
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/doublezz10/manicule/releases/latest", nil)
	req.Header.Set("User-Agent", "manicule/"+currentVersion)
	resp, err := client.Do(req)
	if err != nil {
		return info, nil // offline is not an error worth surfacing
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return info, nil
	}
	tag := extractJSONString(body, "tag_name")
	info.Latest = strings.TrimPrefix(tag, "v")
	info.Update = isNewer(info.Current, info.Latest)
	return info, nil
}

func extractJSONString(body []byte, key string) string {
	hay := `"` + key + `":"`
	i := strings.Index(string(body), hay)
	if i < 0 {
		return ""
	}
	rest := string(body)[i+len(hay):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func isNewer(current, latest string) bool {
	if latest == "" || current == latest {
		return false
	}
	parse := func(s string) ([]int, bool) {
		var out []int
		n := 0
		for _, part := range strings.Split(s, ".") {
			v := 0
			for _, r := range part {
				if r < '0' || r > '9' {
					return out, false
				}
				v = v*10 + int(r-'0')
			}
			out = append(out, v)
		}
		_ = n
		return out, true
	}
	c, okc := parse(current)
	l, okl := parse(latest)
	if !okc || !okl {
		return false
	}
	for i := 0; i < len(c) || i < len(l); i++ {
		cv, lv := 0, 0
		if i < len(c) {
			cv = c[i]
		}
		if i < len(l) {
			lv = l[i]
		}
		if lv > cv {
			return true
		}
		if lv < cv {
			return false
		}
	}
	return false
}

// AttachTray wires the menu-bar tray from main.go.
func (m *Manicule) AttachTray(tray *application.SystemTray) {
	m.tray = tray
	m.refreshTray()
}

func (m *Manicule) refreshTray() {
	if m.tray == nil {
		return
	}
	switch {
	case m.opdsSrv != nil && m.opdsSrv.Running() && m.settings.AuthEnabled:
		m.tray.SetLabel(fmt.Sprintf("☞ %s · PIN %s", opds.LANURL(m.settings.ServerPort), m.settings.Pin))
	case m.opdsSrv != nil && m.opdsSrv.Running():
		m.tray.SetLabel(fmt.Sprintf("☞ %s", opds.LANURL(m.settings.ServerPort)))
	default:
		m.tray.SetLabel("☞ OPDS off")
	}
}

// TrayMenu builds the tray's context menu (called once from main).
func (m *Manicule) TrayMenu() *application.Menu {
	// In server mode (no native window), menu API may be uninitialized.
	defer func() { recover() }()
	menu := application.NewMenu()
	menu.Add("Open manicule").OnClick(func(*application.Context) {
		if w := mainWindow(); w != nil {
			w.Show()
			w.Focus()
		}
	})
	openURL := menu.AddSubmenu("OPDS")
	openURL.Add("Copy library URL").OnClick(func(*application.Context) {
		setClipboard(opds.LANURL(m.settings.ServerPort))
	})
	toggleAuth := menu.AddCheckbox("Require PIN", m.settings.AuthEnabled)
	toggleAuth.OnClick(func(*application.Context) {
		next := !m.settings.AuthEnabled
		m.SaveSettings(SaveSettingsRequest{AuthEnabled: &next})
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		if a := application.Get(); a != nil { a.Quit() }
	})
	return menu
}

var mainWindowRef func() application.Window

func SetMainWindowGetter(fn func() application.Window) { mainWindowRef = fn }
func mainWindow() application.Window {
	if mainWindowRef == nil {
		return nil
	}
	return mainWindowRef()
}
