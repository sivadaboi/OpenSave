//go:build windows

package main

import (
	"context"
	_ "embed"
	"time"

	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

// startTray runs the system tray in its own goroutine (safe on Windows —
// systray owns a private message loop there). Closing the window hides to
// tray; Quit exits for real.
func (a *App) startTray() {
	go systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("OpenSave")
		systray.SetTooltip("OpenSave — game save sync")

		openItem := systray.AddMenuItem("Open OpenSave", "Show the OpenSave window")
		syncItem := systray.AddMenuItem("Sync all games", "Sync every tracked game now")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("Quit", "Stop syncing and exit")

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
	}, nil)
}

func (a *App) stopTray() {
	systray.Quit()
}

func (a *App) showWindow() {
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) quitFromTray() {
	a.reallyQuit = true
	runtime.Quit(a.ctx)
}

// beforeClose intercepts the window X button: hide to tray instead of
// quitting, so syncing keeps running in the background — matching the
// Electron app's minimize-to-tray behavior.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.reallyQuit {
		return false // allow shutdown
	}
	runtime.WindowHide(ctx)
	if a.daemon != nil {
		a.daemon.Log.Log("info", "window hidden to tray; syncing continues in the background")
	}
	return true // prevent close
}
