package store

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/e2ee"
)

func vaultTestStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	if err := s.EnsureDefaultSettings("/data", "/backups"); err != nil {
		t.Fatalf("settings: %v", err)
	}
	return s
}

func TestNoVaultUntilAsked(t *testing.T) {
	s := vaultTestStore(t)
	if _, err := s.LoadKeyring(); !errors.Is(err, ErrNoVault) {
		t.Fatalf("expected ErrNoVault on a fresh device, got %v", err)
	}
	id, err := s.VaultID()
	if err != nil {
		t.Fatalf("vault id: %v", err)
	}
	if id != "" {
		t.Errorf("fresh device already has vault id %q", id)
	}
}

func TestEnsureVaultCreatesOnceAndIsStable(t *testing.T) {
	s := vaultTestStore(t)
	kr, id, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if id == "" {
		t.Fatal("created a vault with no id")
	}
	first, err := kr.Current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	// Asking again returns the same vault rather than minting another.
	kr2, id2, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if id2 != id {
		t.Errorf("vault id changed: %q then %q", id, id2)
	}
	second, _ := kr2.Current()
	if second.ID != first.ID {
		t.Errorf("current key changed: %s then %s", first.ID, second.ID)
	}
	if !bytes.Equal(second.Key, first.Key) {
		t.Error("current key material changed between calls")
	}
	if n := len(kr2.Epochs()); n != 1 {
		t.Errorf("expected 1 epoch, got %d", n)
	}
}

func TestKeyringSurvivesReopen(t *testing.T) {
	s := vaultTestStore(t)
	kr, id, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	ctx := e2ee.Context{VaultID: id, GameID: "g1", Version: 1}
	sealed, keyID, err := kr.Seal([]byte("progress"), ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	reloaded, err := s.LoadKeyring()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err := reloaded.Open(sealed, ctx, keyID)
	if err != nil {
		t.Fatalf("a reloaded keyring could not open what it sealed: %v", err)
	}
	if !bytes.Equal(got, []byte("progress")) {
		t.Error("reloaded keyring opened to the wrong plaintext")
	}
}

// Rotation is only safe if it survives a restart with every prior key intact.
func TestRotatedKeyringPersistsEveryEpoch(t *testing.T) {
	s := vaultTestStore(t)
	kr, id, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	oldCtx := e2ee.Context{VaultID: id, GameID: "g1", Version: 1}
	oldBlob, oldKeyID, err := kr.Seal([]byte("before"), oldCtx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, err := kr.Rotate(time.Now().UTC()); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := s.SaveKeyring(kr); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := s.LoadKeyring()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if n := len(reloaded.Epochs()); n != 2 {
		t.Fatalf("expected 2 epochs after rotation, got %d", n)
	}
	if _, err := reloaded.Open(oldBlob, oldCtx, oldKeyID); err != nil {
		t.Errorf("pre-rotation blob unreadable after reload: %v", err)
	}
	cur, _ := reloaded.Current()
	if cur.ID == oldKeyID {
		t.Error("reload restored the pre-rotation key as current")
	}
}

// The constraint behind the whole design, at the persistence layer: joining a
// vault must not drop the key this device already had history under.
func TestJoinVaultKeepsLocalHistoryReadable(t *testing.T) {
	s := vaultTestStore(t)
	mine, myVaultID, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	myCtx := e2ee.Context{VaultID: myVaultID, GameID: "mine", Version: 1}
	myBlob, myKeyID, err := mine.Seal([]byte("my history"), myCtx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// A peer's vault, with its own history.
	theirKey, _ := e2ee.GenerateVaultKey()
	theirs, err := e2ee.NewKeyring(theirKey, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("peer keyring: %v", err)
	}
	theirCtx := e2ee.Context{VaultID: "peer-vault", GameID: "theirs", Version: 1}
	theirBlob, theirKeyID, err := theirs.Seal([]byte("their history"), theirCtx)
	if err != nil {
		t.Fatalf("peer seal: %v", err)
	}

	merged, err := s.JoinVault("peer-vault", theirs)
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	// Both histories readable, in memory and after a reload.
	for name, kr := range map[string]*e2ee.Keyring{"merged": merged, "": nil} {
		if kr == nil {
			reloaded, err := s.LoadKeyring()
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			kr, name = reloaded, "reloaded"
		}
		if got, err := kr.Open(myBlob, myCtx, myKeyID); err != nil {
			t.Errorf("%s keyring lost this device's own history: %v", name, err)
		} else if !bytes.Equal(got, []byte("my history")) {
			t.Errorf("%s keyring opened local history to the wrong plaintext", name)
		}
		if got, err := kr.Open(theirBlob, theirCtx, theirKeyID); err != nil {
			t.Errorf("%s keyring cannot read the peer's history: %v", name, err)
		} else if !bytes.Equal(got, []byte("their history")) {
			t.Errorf("%s keyring opened peer history to the wrong plaintext", name)
		}
	}

	gotID, err := s.VaultID()
	if err != nil {
		t.Fatalf("vault id: %v", err)
	}
	if gotID != "peer-vault" {
		t.Errorf("vault id = %q, want the joined vault", gotID)
	}
}

func TestJoinVaultOnFreshDeviceAdopts(t *testing.T) {
	s := vaultTestStore(t)
	theirKey, _ := e2ee.GenerateVaultKey()
	theirs, err := e2ee.NewKeyring(theirKey, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("peer keyring: %v", err)
	}
	if _, err := s.JoinVault("peer-vault", theirs); err != nil {
		t.Fatalf("join: %v", err)
	}
	reloaded, err := s.LoadKeyring()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if n := len(reloaded.Epochs()); n != 1 {
		t.Errorf("expected the peer's single key, got %d epochs", n)
	}
	// EnsureVault must not now mint a second vault around the adopted key.
	_, id, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("ensure after join: %v", err)
	}
	if id != "peer-vault" {
		t.Errorf("EnsureVault replaced the joined vault id with %q", id)
	}
}

// SaveKeyring is append-only: a key once stored is never removed, because a
// dropped key is a save this device can no longer open.
func TestSaveKeyringNeverDropsKeys(t *testing.T) {
	s := vaultTestStore(t)
	kr, _, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := kr.Rotate(time.Now().UTC()); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := s.SaveKeyring(kr); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Saving a keyring that holds only ONE of the two keys must not delete
	// the other from the database.
	single, err := e2ee.NewKeyring(kr.Epochs()[0].Key, kr.Epochs()[0].Created)
	if err != nil {
		t.Fatalf("single keyring: %v", err)
	}
	if err := s.SaveKeyring(single); err != nil {
		t.Fatalf("save single: %v", err)
	}
	reloaded, err := s.LoadKeyring()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if n := len(reloaded.Epochs()); n != 2 {
		t.Errorf("saving a partial keyring dropped a key: %d epochs remain, want 2", n)
	}
}

// A re-saved epoch keeps its original Created, since epoch order is what
// Merge uses to pick a winner.
func TestSaveKeyringPreservesOriginalCreated(t *testing.T) {
	s := vaultTestStore(t)
	key, _ := e2ee.GenerateVaultKey()
	early, err := e2ee.NewKeyring(key, time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	if err := s.SaveKeyring(early); err != nil {
		t.Fatalf("save: %v", err)
	}
	late, err := e2ee.NewKeyring(key, time.Unix(900, 0).UTC())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	if err := s.SaveKeyring(late); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	reloaded, err := s.LoadKeyring()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := reloaded.Epochs()[0].Created
	if !got.Equal(time.Unix(100, 0).UTC()) {
		t.Errorf("re-saving moved Created to %v, want the original", got)
	}
}

// A current pointer naming a key this device does not hold must not make
// every stored save unreadable.
func TestLoadRecoversFromDanglingCurrentPointer(t *testing.T) {
	s := vaultTestStore(t)
	kr, id, err := s.EnsureVault()
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	ctx := e2ee.Context{VaultID: id, GameID: "g1", Version: 1}
	sealed, keyID, err := kr.Seal([]byte("still mine"), ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := s.db.Exec(
		`UPDATE settings SET vault_current_key_id = 'ffffffffffffffff' WHERE id = 1`); err != nil {
		t.Fatalf("corrupt pointer: %v", err)
	}

	reloaded, err := s.LoadKeyring()
	if err != nil {
		t.Fatalf("a dangling current pointer made the keyring unloadable: %v", err)
	}
	if _, err := reloaded.Open(sealed, ctx, keyID); err != nil {
		t.Errorf("stored save unreadable after a dangling pointer: %v", err)
	}
	if _, err := reloaded.Current(); err != nil {
		t.Errorf("keyring left with no usable current key: %v", err)
	}
}

func TestSetVaultIDRejectsEmpty(t *testing.T) {
	s := vaultTestStore(t)
	if err := s.SetVaultID(""); err == nil {
		t.Fatal("accepted an empty vault id")
	}
}
