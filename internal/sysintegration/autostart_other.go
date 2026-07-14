//go:build !windows && !linux

package sysintegration

import "errors"

// SetAutostart is unsupported on this platform.
func SetAutostart(enabled bool) error {
	if !enabled {
		return nil
	}
	return errors.New("start-on-boot is not supported on this platform")
}

// AutostartEnabled reports whether autostart is registered.
func AutostartEnabled() bool { return false }
