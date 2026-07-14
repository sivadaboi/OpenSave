package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	opensave "github.com/opensave/opensave"
	"github.com/opensave/opensave/internal/api"
	"github.com/opensave/opensave/internal/daemon"
	"github.com/opensave/opensave/internal/version"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// AppVersion mirrors internal/version.Version — the single source of
// truth for the app version. Keep wails.json's info.productVersion (which
// drives the Windows executable/installer metadata) in sync with it.
var AppVersion = version.Version

// App is the Wails-bound bridge between the webview frontend and the
// embedded daemon. Methods on it are callable from JS.
type App struct {
	ctx         context.Context
	daemon      *daemon.Daemon
	server      *api.Server
	addr        string
	bootErr     string
	reallyQuit  bool
	updatedFrom string // previous version when this run is the first on a new build
}

// NewApp creates the App shell (daemon boots in startup).
func NewApp() *App {
	return &App{}
}

// startup boots the daemon + local API server once the webview exists.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	d, err := daemon.New(daemon.Options{})
	if err != nil {
		a.bootErr = err.Error()
		return
	}
	a.daemon = d

	if err := d.Start(); err != nil {
		a.bootErr = err.Error()
		d.Stop()
		return
	}

	// Detect "first run after an update" so the UI can greet with the
	// changelog — this is how users see what's new after a peer-to-peer
	// update, which carries no release notes of its own.
	a.updatedFrom = stampVersionFile(d.Paths.HomeDir)
	if a.updatedFrom != "" {
		d.Log.Log("success", "OpenSave updated: "+a.updatedFrom+" → "+AppVersion)
	}

	settings, err := d.Store.GetSettings()
	if err != nil {
		a.bootErr = err.Error()
		d.Stop()
		return
	}

	a.server = api.New(d)
	addr, err := a.server.Start(settings.Port)
	if err != nil {
		// Configured port taken (another instance?) — fall back to an
		// ephemeral port; the frontend asks us for the real address.
		addr, err = a.server.Start(0)
		if err != nil {
			a.bootErr = err.Error()
			d.Stop()
			return
		}
	}
	// The listener binds 0.0.0.0 (P2P routes are LAN-reachable), but the
	// webview must dial loopback — "0.0.0.0" is not a connectable host in
	// browsers.
	if _, port, splitErr := net.SplitHostPort(addr); splitErr == nil {
		addr = "127.0.0.1:" + port
	}

	// Self-check: confirm the API is actually connectable on loopback
	// before handing the address to the webview. If something on this
	// machine blocks it (firewall/security software), fail loudly with the
	// reason instead of letting the UI render broken with "Failed to
	// fetch" on every call.
	if conn, dialErr := net.DialTimeout("tcp", addr, 3*time.Second); dialErr != nil {
		a.bootErr = fmt.Sprintf(
			"the app's local service started on %s but this machine blocks connections to it (%v) — check firewall/security software",
			addr, dialErr)
		d.Log.Log("error", a.bootErr)
		return
	} else {
		conn.Close()
	}

	a.addr = addr
	d.Log.Log("info", "desktop app connected to daemon at "+addr)

	a.startTray()
}

// onSecondInstanceLaunch fires when OpenSave is launched again while it is
// already running — commonly because closing the window only hides it to the
// tray, so the user thinks it's closed. Rather than spawn a second (blank)
// window, surface the existing one.
func (a *App) onSecondInstanceLaunch(_ options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) shutdown(ctx context.Context) {
	a.stopTray()
	if a.server != nil {
		a.server.Stop()
	}
	if a.daemon != nil {
		a.daemon.Stop()
	}
}

// AppInfo returns static app metadata for the About dialog / status bar.
func (a *App) AppInfo() map[string]string {
	return map[string]string{
		"name":      "OpenSave",
		"version":   AppVersion,
		"buildTime": strconv.FormatInt(version.BuildTimeMs(), 10),
		"tagline":   "Peer-to-peer game save sync",
		"license":   "MIT",
		"copyright": "© 2026 Siva Prakash & OpenSave contributors",
		"tech":      "Go + Wails",
	}
}

// Changelog returns the embedded CHANGELOG.md for the in-app "What's new"
// view.
func (a *App) Changelog() string {
	return opensave.Changelog
}

// UpdateGreeting reports the version this install was just updated FROM
// ("" normally) so the UI can announce the update and offer the changelog.
func (a *App) UpdateGreeting() map[string]string {
	return map[string]string{"updatedFrom": a.updatedFrom}
}

// DaemonAddr returns the local API address (host:port) or an error string
// if the daemon failed to boot.
func (a *App) DaemonAddr() map[string]string {
	if a.bootErr != "" {
		return map[string]string{"error": a.bootErr}
	}
	return map[string]string{"addr": a.addr}
}

// SelectDirectory opens the native folder picker.
func (a *App) SelectDirectory(title string) string {
	if title == "" {
		title = "Select folder"
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
	if err != nil {
		return ""
	}
	return dir
}

// SelectFile opens the native file picker (executables for game launch).
func (a *App) SelectFile(title string) string {
	if title == "" {
		title = "Select file"
	}
	file, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
		Filters: []runtime.FileFilter{
			{DisplayName: "Programs (*.exe;*.bat)", Pattern: "*.exe;*.bat"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
	if err != nil {
		return ""
	}
	return file
}

// SelectSaveFile opens the native save dialog (backup export target).
func (a *App) SelectSaveFile(title, defaultName string) string {
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
		Filters: []runtime.FileFilter{
			{DisplayName: "OpenSave backup (*.sscb)", Pattern: "*.sscb"},
		},
	})
	if err != nil {
		return ""
	}
	return file
}

// OpenExternal opens a URL in the system browser (Steam pages, OAuth).
func (a *App) OpenExternal(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
}

// ShowWindow surfaces the window (un-hides from tray, un-minimises, and
// brings it to the front). Called from the UI when an event needs the
// user's attention, e.g. an incoming pairing request.
func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

// Window controls for the custom title bar.
func (a *App) WindowMinimise() { runtime.WindowMinimise(a.ctx) }
func (a *App) WindowToggleMaximise() {
	runtime.WindowToggleMaximise(a.ctx)
}
func (a *App) WindowClose() { runtime.Quit(a.ctx) }

var _ = fmt.Sprintf // reserved
