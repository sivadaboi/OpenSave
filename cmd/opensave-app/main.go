// Command opensave-app is the OpenSave desktop application: the daemon
// embedded in-process behind a Wails (webview) UI.
package main

import (
	"embed"
	"fmt"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	// WebKitGTK's DMA-BUF renderer instantly crashes or blanks the window
	// on a range of GPU/driver combos (AMD handhelds like the ROG Ally,
	// NVIDIA+Wayland). Shared-memory rendering is imperceptibly slower for
	// a UI like ours and works everywhere — default to it, but respect an
	// explicit user override of the variable.
	if runtime.GOOS == "linux" && os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
		os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}

	// After a self-update this waits for the replaced process to exit (and
	// removes its leftover binary) before claiming the single-instance lock.
	cleanupReplacedBinary()

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "OpenSave",
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 12, G: 12, B: 13, A: 1},
		Frameless:        true,
		// Only one OpenSave window ever; a second launch focuses the existing
		// one instead of opening a duplicate (blank) window.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "opensave-desktop-single-instance",
			OnSecondInstanceLaunch: app.onSecondInstanceLaunch,
		},
		OnStartup:     app.startup,
		OnShutdown:    app.shutdown,
		OnBeforeClose: app.beforeClose,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		Linux: &linux.Options{
			// Window/taskbar icon — without this, Linux shows a blank icon.
			Icon: appIcon,
			// Must match the .desktop file's StartupWMClass/Name so docks
			// group the running window with its launcher entry.
			ProgramName:         "OpenSave",
			WindowIsTranslucent: false,
			WebviewGpuPolicy:    linux.WebviewGpuPolicyOnDemand,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "opensave: %v\n", err)
		os.Exit(1)
	}
}
