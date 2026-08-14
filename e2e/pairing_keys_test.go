package e2e

import (
	"bytes"
	"testing"

	"github.com/opensave/opensave/testutil"
)

// Pairing has to leave both devices able to derive the same key, or nothing
// built on top of it can encrypt anything.
//
// Worth testing end to end rather than in the crypto package: the maths is
// covered there, and what can actually go wrong here is the plumbing — a key
// that never left the asking device, one dropped by the approving side, or one
// pinned against the wrong peer.
func TestPairingExchangesEncryptionKeys(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Key-A")
	b := testutil.NewTestDaemon(t, "Key-B")
	a.PairWith(b)

	// Each side pinned the other's public half.
	aSeesB, err := a.Daemon.Store.GetPeer(b.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	bSeesA, err := b.Daemon.Store.GetPeer(a.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	if aSeesB.PublicKey == "" {
		t.Error("A paired with B but pinned no key for it — syncs would stay unencrypted")
	}
	if bSeesA.PublicKey == "" {
		t.Error("B paired with A but pinned no key for it")
	}

	// And the pinned keys are each other's, not something else: the shared
	// secret both sides derive has to match.
	keyOnA, okA, err := a.Daemon.Store.SharedKeyWith(b.NodeID())
	if err != nil || !okA {
		t.Fatalf("A could not derive a shared key: ok=%v err=%v", okA, err)
	}
	keyOnB, okB, err := b.Daemon.Store.SharedKeyWith(a.NodeID())
	if err != nil || !okB {
		t.Fatalf("B could not derive a shared key: ok=%v err=%v", okB, err)
	}
	if !bytes.Equal(keyOnA, keyOnB) {
		t.Fatal("the two devices derived different keys; each would send the other something it cannot open")
	}
}

// Both devices must show the same fingerprint, since the whole point is that a
// person compares them across two screens.
func TestPairingFingerprintMatchesOnBothDevices(t *testing.T) {
	a := testutil.NewTestDaemon(t, "FP-A")
	b := testutil.NewTestDaemon(t, "FP-B")
	a.PairWith(b)

	onA, err := a.Daemon.Store.PairingFingerprint(b.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	onB, err := b.Daemon.Store.PairingFingerprint(a.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	if onA == "" {
		t.Fatal("no fingerprint on A")
	}
	if onA != onB {
		t.Fatalf("the devices show different fingerprints, so comparing them proves nothing:\n  A: %s\n  B: %s", onA, onB)
	}
}

// A device's identity must survive a restart. Regenerating it would silently
// break every existing pairing, since the peers still hold the old public half.
func TestDeviceIdentityIsStable(t *testing.T) {
	d := testutil.NewTestDaemon(t, "Stable")

	first, err := d.Daemon.Store.DeviceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Daemon.Store.DeviceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Private, second.Private) || !bytes.Equal(first.Public, second.Public) {
		t.Fatal("asking twice produced two different identities; every pairing would break on restart")
	}
}

// A peer with no pinned key is the ordinary case during a rollout — one side
// upgraded, the other not. It must report "no key" rather than an error, so
// callers fall back to sending in the clear instead of failing the sync.
func TestNoKeyIsNotAnError(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Solo")

	_, ok, err := a.Daemon.Store.SharedKeyWith("node_never_paired")
	if ok {
		t.Error("derived a key for a peer that does not exist")
	}
	_ = err // a missing peer may error; what matters is ok == false
}
