package p2p

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
)

func newPresenceTestClient(t *testing.T) (*WanClient, *store.Store, *[]string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("EnsureDefaultSettings error = %v", err)
	}

	var logs []string
	e := &Engine{Store: s, Log: func(level, msg string) {
		logs = append(logs, level+": "+msg)
	}}
	e.Wan = newWanClient(e)
	return e.Wan, s, &logs
}

// TestHasDiscovered covers the question LAN expiry asks before declaring a
// peer gone: is the relay still hearing from it? A peer that has gone quiet
// past the expiry window must read as absent, or a stale entry would keep a
// genuinely offline device looking online forever.
func TestHasDiscovered(t *testing.T) {
	w, _, _ := newPresenceTestClient(t)

	if w.HasDiscovered("node_absent") {
		t.Error("a peer never seen in the room must not report as discovered")
	}

	w.recordDiscovered(RelayMessage{From: "node_live", DeviceName: "Omar"})
	if !w.HasDiscovered("node_live") {
		t.Error("a peer that just announced itself should report as discovered")
	}

	// Age it past the expiry window: present in the map, but silent too long.
	w.mu.Lock()
	p := w.discovered["node_live"]
	p.LastSeen = time.Now().Add(-2 * wanPeerExpiry).UnixMilli()
	w.discovered["node_live"] = p
	w.mu.Unlock()

	if w.HasDiscovered("node_live") {
		t.Error("a peer silent past the expiry window must not report as discovered")
	}
}

// TestWarnIfStalePairing pins the reinstall case: a wiped device rejoins
// under a fresh node ID, so the pairing row keyed on its old ID can never
// match again and sits at "offline" while the device is plainly in the room.
// The app has to say so — and say it once, not on every hello.
func TestWarnIfStalePairing(t *testing.T) {
	w, s, logs := newPresenceTestClient(t)

	if err := s.UpsertPeer(store.Peer{
		ID: "node_old", Name: "Omar", DeviceType: "desktop",
		Address: "relay", Status: "offline",
	}); err != nil {
		t.Fatalf("UpsertPeer error = %v", err)
	}

	// Same device name, new identity: the user needs to know why their
	// online device reads as offline.
	w.warnIfStalePairing(RelayMessage{From: "node_new", DeviceName: "Omar"})
	if len(*logs) != 1 {
		t.Fatalf("expected one warning, got %d: %v", len(*logs), *logs)
	}
	if !strings.Contains((*logs)[0], "Omar") || !strings.Contains((*logs)[0], "Unpair") {
		t.Errorf("warning should name the device and the fix, got %q", (*logs)[0])
	}

	// Every hello would otherwise repeat it — the relay sends one per
	// reconnect, and pings keep the room busy.
	w.warnIfStalePairing(RelayMessage{From: "node_new", DeviceName: "Omar"})
	if len(*logs) != 1 {
		t.Errorf("warning should fire once per peer, got %d: %v", len(*logs), *logs)
	}
}

// TestWarnIfStalePairingIgnoresUnrelated guards the two ways this could cry
// wolf: a device we hold no pairing for at all, and the paired device itself
// reconnecting normally under the ID we already know.
func TestWarnIfStalePairingIgnoresUnrelated(t *testing.T) {
	w, s, logs := newPresenceTestClient(t)

	if err := s.UpsertPeer(store.Peer{
		ID: "node_old", Name: "Omar", DeviceType: "desktop",
		Address: "relay", Status: "offline",
	}); err != nil {
		t.Fatalf("UpsertPeer error = %v", err)
	}

	w.warnIfStalePairing(RelayMessage{From: "node_x", DeviceName: "Somebody Else"})
	if len(*logs) != 0 {
		t.Errorf("an unrelated device must not trigger a stale-pairing warning: %v", *logs)
	}

	w.warnIfStalePairing(RelayMessage{From: "node_old", DeviceName: "Omar"})
	if len(*logs) != 0 {
		t.Errorf("the paired device's own ID must not read as a stale pairing: %v", *logs)
	}

	// A nameless hello carries nothing to match on and must stay quiet.
	w.warnIfStalePairing(RelayMessage{From: "node_new", DeviceName: ""})
	if len(*logs) != 0 {
		t.Errorf("a hello with no device name must not warn: %v", *logs)
	}
}
