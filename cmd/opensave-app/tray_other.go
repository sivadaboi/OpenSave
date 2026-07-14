//go:build !windows

package main

import "context"

// Tray integration is Windows-only for now: on Linux, getlantern/systray
// needs its own GTK main loop, which conflicts with the one Wails owns.
// Closing the window quits normally there.
func (a *App) startTray() {}
func (a *App) stopTray()  {}

func (a *App) beforeClose(ctx context.Context) bool { return false }
