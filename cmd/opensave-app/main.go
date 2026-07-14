// Command opensave-app is the OpenSave desktop application: the daemon
// embedded in-process behind a Wails (webview) UI.
package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
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
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "opensave: %v\n", err)
		os.Exit(1)
	}
}
