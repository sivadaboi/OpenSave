package e2ee

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func mustKeyring(t *testing.T, created time.Time) (*Keyring, []byte) {
	t.Helper()
	key, err := GenerateVaultKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	kr, err := NewKeyring(key, created)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	return kr, key
}

func TestKeyIDIsStableAndHidesTheKey(t *testing.T) {
	key, _ := GenerateVaultKey()
	a, err := KeyID(key)
	if err != nil {
		t.Fatalf("key id: %v", err)
	}
	b, _ := KeyID(key)
	if a != b {
		t.Error("the same key produced two different IDs")
	}
	other, _ := GenerateVaultKey()
	if c, _ := KeyID(other); c == a {
		t.Error("two different keys produced the same ID")
	}
	// The ID travels in the clear beside every blob.
	if strings.Contains(a, string(key)) || bytes.Contains([]byte(a), key) {
		t.Error("key ID leaks the key")
	}
	if _, err := KeyID(make([]byte, 16)); err == nil {
		t.Error("accepted a wrong-sized key")
	}
}

func TestKeyringSealOpenRoundTrip(t *testing.T) {
	kr, _ := mustKeyring(t, time.Now())
	ctx := Context{VaultID: "v1", GameID: "g1", Version: 1}
	sealed, keyID, err := kr.Seal([]byte("progress"), ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	got, err := kr.Open(sealed, ctx, keyID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, []byte("progress")) {
		t.Error("round trip changed the payload")
	}
}

// The point of the whole design: rotating must never strand what came before.
func TestRotateKeepsOldBlobsReadable(t *testing.T) {
	kr, _ := mustKeyring(t, time.Unix(100, 0))
	oldCtx := Context{VaultID: "v1", GameID: "g1", Version: 1}
	oldBlob, oldID, err := kr.Seal([]byte("before rotation"), oldCtx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	newEpoch, err := kr.Rotate(time.Unix(200, 0))
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newEpoch.ID == oldID {
		t.Fatal("rotation produced the same key")
	}
	cur, _ := kr.Current()
	if cur.ID != newEpoch.ID {
		t.Error("rotation did not move the current pointer")
	}

	// New seals use the new key...
	_, freshID, err := kr.Seal([]byte("after"), Context{VaultID: "v1", GameID: "g1", Version: 2})
	if err != nil {
		t.Fatalf("seal after rotate: %v", err)
	}
	if freshID != newEpoch.ID {
		t.Error("new seal did not use the rotated key")
	}
	// ...and the pre-rotation blob is still readable.
	got, err := kr.Open(oldBlob, oldCtx, oldID)
	if err != nil {
		t.Fatalf("pre-rotation blob became unreadable: %v", err)
	}
	if !bytes.Equal(got, []byte("before rotation")) {
		t.Error("pre-rotation blob opened to the wrong plaintext")
	}
}

// The constraint that drove this design: when two populated vaults merge,
// neither side's history may become unreadable.
func TestMergeLosesNothingFromEitherSide(t *testing.T) {
	older, _ := mustKeyring(t, time.Unix(100, 0))
	newer, _ := mustKeyring(t, time.Unix(500, 0))

	ctxA := Context{VaultID: "vA", GameID: "gA", Version: 1}
	ctxB := Context{VaultID: "vB", GameID: "gB", Version: 1}
	blobA, idA, err := older.Seal([]byte("device A history"), ctxA)
	if err != nil {
		t.Fatalf("seal A: %v", err)
	}
	blobB, idB, err := newer.Seal([]byte("device B history"), ctxB)
	if err != nil {
		t.Fatalf("seal B: %v", err)
	}

	if err := newer.Merge(older); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := older.Merge(newer); err != nil {
		t.Fatalf("reverse merge: %v", err)
	}

	// Both devices can now read both histories.
	for name, kr := range map[string]*Keyring{"A": older, "B": newer} {
		got, err := kr.Open(blobA, ctxA, idA)
		if err != nil {
			t.Errorf("device %s lost access to A's history: %v", name, err)
		} else if !bytes.Equal(got, []byte("device A history")) {
			t.Errorf("device %s opened A's blob to the wrong plaintext", name)
		}
		if got, err := kr.Open(blobB, ctxB, idB); err != nil {
			t.Errorf("device %s lost access to B's history: %v", name, err)
		} else if !bytes.Equal(got, []byte("device B history")) {
			t.Errorf("device %s opened B's blob to the wrong plaintext", name)
		}
	}
}

// Both sides must independently reach the same answer, or they would seal
// future blobs under different keys and quietly diverge.
func TestMergeIsDeterministicAndPrefersTheOlderVault(t *testing.T) {
	older, olderKey := mustKeyring(t, time.Unix(100, 0))
	newer, _ := mustKeyring(t, time.Unix(500, 0))
	olderID, _ := KeyID(olderKey)

	if err := newer.Merge(older); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := older.Merge(newer); err != nil {
		t.Fatalf("reverse merge: %v", err)
	}

	a, _ := older.Current()
	b, _ := newer.Current()
	if a.ID != b.ID {
		t.Fatalf("the two sides disagree on the current key: %s vs %s", a.ID, b.ID)
	}
	if a.ID != olderID {
		t.Error("the older vault's key should stay current")
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	a, _ := mustKeyring(t, time.Unix(100, 0))
	b, _ := mustKeyring(t, time.Unix(200, 0))
	if err := a.Merge(b); err != nil {
		t.Fatalf("merge: %v", err)
	}
	first := len(a.Epochs())
	firstCurrent, _ := a.Current()
	for i := 0; i < 3; i++ {
		if err := a.Merge(b); err != nil {
			t.Fatalf("re-merge: %v", err)
		}
	}
	if got := len(a.Epochs()); got != first {
		t.Errorf("re-merging grew the keyring from %d to %d", first, got)
	}
	if cur, _ := a.Current(); cur.ID != firstCurrent.ID {
		t.Error("re-merging changed the current key")
	}
}

// A device that never learned a key must say so plainly, not fail as if the
// data were corrupt — the user's next step is to pair or recover, and the
// message has to point there.
func TestOpenWithUnknownKeyIDExplainsItself(t *testing.T) {
	kr, _ := mustKeyring(t, time.Now())
	stranger, strangerKey := mustKeyring(t, time.Now())
	ctx := Context{VaultID: "v1", GameID: "g1", Version: 1}
	sealed, keyID, err := stranger.Seal([]byte("x"), ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	_, err = kr.Open(sealed, ctx, keyID)
	if err == nil {
		t.Fatal("opened a blob sealed under a key this device never held")
	}
	if !strings.Contains(err.Error(), "recovery passphrase") {
		t.Errorf("error should point at the way out, got %q", err)
	}
	// Once the key is learned, the same blob opens.
	if _, err := kr.Add(strangerKey, time.Now()); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := kr.Open(sealed, ctx, keyID); err != nil {
		t.Errorf("blob still unreadable after learning its key: %v", err)
	}
}

// Epoch ordering is what Merge uses to pick a winner, so a key re-learned
// later must not appear younger than it is.
func TestAddKeepsEarliestCreated(t *testing.T) {
	kr, key := mustKeyring(t, time.Unix(500, 0))
	id, err := kr.Add(key, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	e, ok := kr.Get(id)
	if !ok {
		t.Fatal("key vanished after re-add")
	}
	if !e.Created.Equal(time.Unix(100, 0)) {
		t.Errorf("re-adding did not keep the earlier time, got %v", e.Created)
	}
	if len(kr.Epochs()) != 1 {
		t.Error("re-adding the same key created a second epoch")
	}
}

func TestEpochsAreOldestFirst(t *testing.T) {
	kr, _ := mustKeyring(t, time.Unix(300, 0))
	k2, _ := GenerateVaultKey()
	k3, _ := GenerateVaultKey()
	if _, err := kr.Add(k2, time.Unix(100, 0)); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := kr.Add(k3, time.Unix(200, 0)); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := kr.Epochs()
	if len(got) != 3 {
		t.Fatalf("expected 3 epochs, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Created.Before(got[i-1].Created) {
			t.Errorf("epochs out of order at %d", i)
		}
	}
}

func TestSetCurrentRejectsUnheldKey(t *testing.T) {
	kr, _ := mustKeyring(t, time.Now())
	if err := kr.SetCurrent("00000000deadbeef"); err == nil {
		t.Fatal("pointed current at a key the ring does not hold")
	}
}

// A keyring must not hand out a reference to its own key material.
func TestKeyringCopiesKeyMaterial(t *testing.T) {
	key, _ := GenerateVaultKey()
	kr, err := NewKeyring(key, time.Now())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	original := append([]byte{}, key...)
	for i := range key {
		key[i] ^= 0xff
	}
	cur, _ := kr.Current()
	if !bytes.Equal(cur.Key, original) {
		t.Error("mutating the caller's slice changed the stored key")
	}
}
