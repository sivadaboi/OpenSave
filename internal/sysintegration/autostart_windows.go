//go:build windows

// Package sysintegration handles OS-level integration: start-on-boot
// registration and (on Windows) the system tray.
package sysintegration

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValueName = "OpenSave"

// SetAutostart registers or removes OpenSave in the current user's Run
// key (no admin rights needed).
func SetAutostart(enabled bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer key.Close()

	if !enabled {
		err := key.DeleteValue(runValueName)
		if err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("remove autostart entry: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if err := key.SetStringValue(runValueName, `"`+exe+`"`); err != nil {
		return fmt.Errorf("set autostart entry: %w", err)
	}
	return nil
}

// AutostartEnabled reports whether the Run entry exists.
func AutostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetStringValue(runValueName)
	return err == nil
}
