//go:build linux

package sysintegration

import (
	"fmt"
	"os"
	"path/filepath"
)

// desktopEntry mirrors the .desktop file the Electron app wrote.
const desktopEntry = `[Desktop Entry]
Type=Application
Name=OpenSave
Comment=P2P game save sync
Exec=%s
Terminal=false
X-GNOME-Autostart-enabled=true
`

func autostartFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "autostart", "opensave.desktop"), nil
}

// SetAutostart writes or removes ~/.config/autostart/opensave.desktop.
func SetAutostart(enabled bool) error {
	path, err := autostartFilePath()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove autostart entry: %w", err)
		}
		return nil
	}

	// Inside a Flatpak sandbox os.Executable() is /app/bin/opensave, a path
	// that doesn't exist on the host session that runs autostart entries —
	// launch through flatpak instead.
	launch := ""
	if id := os.Getenv("FLATPAK_ID"); id != "" {
		launch = "flatpak run " + id
	} else {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
		launch = exe
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf(desktopEntry, launch)), 0o644)
}

// AutostartEnabled reports whether the .desktop file exists.
func AutostartEnabled() bool {
	path, err := autostartFilePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}
