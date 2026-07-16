//go:build !windows && !linux

package main

import "context"

// No tray on this platform (macOS would need main-thread NSApplication
// integration). Closing the window quits normally.
func (a *App) startTray() {}
func (a *App) stopTray()  {}

func (a *App) beforeClose(ctx context.Context) bool { return false }
