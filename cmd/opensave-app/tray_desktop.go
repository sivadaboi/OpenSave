//go:build windows || linux

package main

import (
	"context"
	_ "embed"
	"runtime"
	"sync/atomic"
	"time"

	"fyne.io/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// System tray for Windows and Linux.
//
// fyne.io/systray (the maintained getlantern fork) owns a private message
// loop on Windows and speaks pure-Go D-Bus StatusNotifierItem on Linux —
// neither conflicts with the GTK/Win32 main loop Wails owns, so it can run
// in its own goroutine on both platforms.
//
// Not every Linux desktop has a StatusNotifier host (stock GNOME needs an
// extension). trayReady tracks whether the tray actually appeared: closing
// the window only hides to tray when there IS a tray to reopen from —
// otherwise it quits normally, so the app can never become unreachable.

//go:embed build/windows/icon.ico
var trayIconICO []byte

//go:embed build/appicon.png
var trayIconPNG []byte

// trayReady is true once the tray icon is actually up.
var trayReady atomic.Bool

func trayIconBytes() []byte {
	if runtime.GOOS == "windows" {
		return trayIconICO // Windows wants ICO
	}
	return trayIconPNG // Linux StatusNotifier wants PNG
}

// startTray runs the system tray in its own goroutine.
func (a *App) startTray() {
	go systray.Run(func() {
		systray.SetIcon(trayIconBytes())
		systray.SetTitle("OpenSave")
		systray.SetTooltip("OpenSave — game save sync")

		openItem := systray.AddMenuItem("Open OpenSave", "Show the OpenSave window")
		syncItem := systray.AddMenuItem("Sync all games", "Sync every tracked game now")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("Quit", "Stop syncing and exit")

		trayReady.Store(true)

		go func() {
			for {
				select {
				case <-openItem.ClickedCh:
					a.showWindow()
				case <-syncItem.ClickedCh:
					if a.daemon != nil {
						go func() {
							ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
							defer cancel()
							a.daemon.P2P.SyncAllGames(ctx)
						}()
					}
				case <-quitItem.ClickedCh:
					a.quitFromTray()
				}
			}
		}()
	}, func() { trayReady.Store(false) })
}

func (a *App) stopTray() {
	systray.Quit()
}

func (a *App) showWindow() {
	wailsruntime.WindowShow(a.ctx)
	wailsruntime.WindowUnminimise(a.ctx)
}

func (a *App) quitFromTray() {
	a.reallyQuit = true
	wailsruntime.Quit(a.ctx)
}

// beforeClose intercepts the window X button: hide to tray instead of
// quitting, so syncing keeps running in the background. If the tray never
// materialized (Linux DE without a StatusNotifier host), fall through to a
// normal quit — a hidden window with no tray would be unreachable.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.reallyQuit {
		return false // allow shutdown
	}
	if !trayReady.Load() {
		return false // no tray to come back from — quit normally
	}
	wailsruntime.WindowHide(ctx)
	if a.daemon != nil {
		a.daemon.Log.Log("info", "window hidden to tray; syncing continues in the background")
	}
	return true // prevent close
}
