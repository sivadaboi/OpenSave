package api

import (
	"net"
	"strconv"
	"testing"

	"github.com/opensave/opensave/internal/daemon"
)

// Pairing tells the other device to call back on settings.Port, and that
// callback is what completes the handshake on the side that started it. If
// the daemon lands on a different port than the configured one — the
// configured port was taken, or the CLI was given --port — settings must
// follow, or the initiating device is left showing no paired peers while the
// approving device thinks everything worked.
//
// Reported in the wild as "pairing shows the approve popup on my Deck but my
// PC says no device paired", with internet sync working fine, because
// relay-routed peers are never addressed by port.
func TestStartRecordsTheBoundPortInSettings(t *testing.T) {
	home := t.TempDir()
	d, err := daemon.New(daemon.Options{HomeOverride: home, DisableDiscovery: true})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	defer d.Stop()
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}

	before, err := d.Store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}

	srv := New(d)
	addr, err := srv.Start(0) // 0 => OS picks, guaranteed != configured
	if err != nil {
		t.Fatalf("api.Start: %v", err)
	}
	defer srv.Stop()

	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	after, err := d.Store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := strconv.Itoa(after.Port); got != portStr {
		t.Errorf("settings.Port = %s after binding %s (was %d) — peers would be told to "+
			"call back on a port nothing is listening on", got, addr, before.Port)
	}
}
