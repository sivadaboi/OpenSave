package e2ee

import (
	"bytes"
	"strings"
	"testing"
)

// pairFor builds two identities and the key they share, the way pairing does.
func pairFor(t *testing.T) (a, b Identity, shared []byte) {
	t.Helper()
	a, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("generate A: %v", err)
	}
	b, err = GenerateIdentity()
	if err != nil {
		t.Fatalf("generate B: %v", err)
	}
	shared, err = SharedKey(a.Private, b.Public)
	if err != nil {
		t.Fatalf("shared key: %v", err)
	}
	return a, b, shared
}

// cheapParams keeps Argon2id honest but fast enough to run in a unit test.
// DefaultRecoveryParams is exercised separately.
func cheapParams(t *testing.T) RecoveryParams {
	t.Helper()
	p, err := DefaultRecoveryParams()
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	p.Time, p.MemoryK, p.Threads = 1, 8*1024, 1
	return p
}

func TestVaultKeyWrapRoundTrip(t *testing.T) {
	_, _, shared := pairFor(t)
	vk, err := GenerateVaultKey()
	if err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	wrapped, err := WrapVaultKey(shared, vk)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if bytes.Contains(wrapped, vk) {
		t.Fatal("wrapped blob contains the raw vault key")
	}
	got, err := UnwrapVaultKey(shared, wrapped)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, vk) {
		t.Error("unwrapped vault key differs from the original")
	}
}

func TestVaultKeyWrapRejectsWrongPairing(t *testing.T) {
	_, _, shared := pairFor(t)
	_, _, other := pairFor(t)
	vk, _ := GenerateVaultKey()
	wrapped, err := WrapVaultKey(shared, vk)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if _, err := UnwrapVaultKey(other, wrapped); err == nil {
		t.Fatal("an unrelated pairing key unwrapped the vault key")
	}
}

// This is the property the whole vault design exists for: one ciphertext that
// every device in the vault can read, which pairwise keys cannot produce.
func TestVaultKeyLetsEveryDeviceReadOneBlob(t *testing.T) {
	vk, err := GenerateVaultKey()
	if err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	// A mints the vault and wraps it for B and for C under separate pairings.
	_, _, sharedAB := pairFor(t)
	_, _, sharedAC := pairFor(t)
	wrappedForB, err := WrapVaultKey(sharedAB, vk)
	if err != nil {
		t.Fatalf("wrap for B: %v", err)
	}
	wrappedForC, err := WrapVaultKey(sharedAC, vk)
	if err != nil {
		t.Fatalf("wrap for C: %v", err)
	}
	vkB, err := UnwrapVaultKey(sharedAB, wrappedForB)
	if err != nil {
		t.Fatalf("B unwrap: %v", err)
	}
	vkC, err := UnwrapVaultKey(sharedAC, wrappedForC)
	if err != nil {
		t.Fatalf("C unwrap: %v", err)
	}

	ctx := Context{VaultID: "v1", GameID: "game-42", Version: 7}
	save := []byte("hard-won progress")
	sealed, err := SealFor(vk, save, ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// The single stored blob opens for both other devices.
	for name, key := range map[string][]byte{"B": vkB, "C": vkC} {
		got, err := OpenFor(key, sealed, ctx)
		if err != nil {
			t.Fatalf("device %s could not open the shared blob: %v", name, err)
		}
		if !bytes.Equal(got, save) {
			t.Errorf("device %s opened the blob to the wrong plaintext", name)
		}
	}
}

func TestSealForBindsIdentity(t *testing.T) {
	vk, _ := GenerateVaultKey()
	ctx := Context{VaultID: "v1", GameID: "game-A", Version: 3}
	sealed, err := SealFor(vk, []byte("save A"), ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// A storing server can serve the wrong blob; each of these is that
	// substitution, and each must fail rather than decrypt.
	wrong := map[string]Context{
		"different game":   {VaultID: "v1", GameID: "game-B", Version: 3},
		"replayed version": {VaultID: "v1", GameID: "game-A", Version: 2},
		"advanced version": {VaultID: "v1", GameID: "game-A", Version: 4},
		"different vault":  {VaultID: "v2", GameID: "game-A", Version: 3},
	}
	for name, bad := range wrong {
		if _, err := OpenFor(vk, sealed, bad); err == nil {
			t.Errorf("%s: opened a blob that was sealed for a different identity", name)
		}
	}

	// The exact identity still opens.
	if _, err := OpenFor(vk, sealed, ctx); err != nil {
		t.Errorf("correct context failed to open: %v", err)
	}
}

// Length-prefixing the AAD fields is what stops one identity from being
// spelled as another; without it ("ab","c") and ("a","bc") collide.
func TestContextAADIsUnambiguous(t *testing.T) {
	x := Context{VaultID: "ab", GameID: "c", Version: 1}
	y := Context{VaultID: "a", GameID: "bc", Version: 1}
	if bytes.Equal(x.aad(), y.aad()) {
		t.Fatal("two distinct identities produced identical associated data")
	}
}

func TestSealForRequiresIdentity(t *testing.T) {
	vk, _ := GenerateVaultKey()
	for _, ctx := range []Context{
		{GameID: "g", Version: 1},
		{VaultID: "v", Version: 1},
		{},
	} {
		if _, err := SealFor(vk, []byte("x"), ctx); err == nil {
			t.Errorf("sealed with an incomplete context %+v", ctx)
		}
	}
}

// Domain separation: a wrapped key and a sealed payload must never be
// interchangeable, even under the same key bytes.
func TestVaultDomainsDoNotCross(t *testing.T) {
	_, _, shared := pairFor(t)
	vk, _ := GenerateVaultKey()

	wrapped, err := WrapVaultKey(shared, vk)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	// A wrapped vault key must not open as a plain pairwise payload.
	if _, err := Open(shared, wrapped); err == nil {
		t.Error("a wrapped vault key opened as an ordinary payload")
	}

	plain, err := Seal(shared, []byte("ordinary"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// An ordinary payload must not unwrap as a vault key.
	if _, err := UnwrapVaultKey(shared, plain); err == nil {
		t.Error("an ordinary payload unwrapped as a vault key")
	}
}

func TestRecoveryPassphraseRoundTrip(t *testing.T) {
	p := cheapParams(t)
	vk, _ := GenerateVaultKey()
	blob, err := WrapVaultKeyPassphrase("correct horse battery staple", vk, p)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := UnwrapVaultKeyPassphrase("correct horse battery staple", blob, p)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(got, vk) {
		t.Error("recovered vault key differs from the original")
	}
}

func TestRecoveryRejectsWrongPassphrase(t *testing.T) {
	p := cheapParams(t)
	vk, _ := GenerateVaultKey()
	blob, err := WrapVaultKeyPassphrase("right", vk, p)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	_, err = UnwrapVaultKeyPassphrase("wrong", blob, p)
	if err == nil {
		t.Fatal("the wrong passphrase recovered the vault key")
	}
	if !strings.Contains(err.Error(), "passphrase") {
		t.Errorf("error should point at the passphrase, got %q", err)
	}
}

// A blob is only recoverable with the params that made it, so those params
// have to be stored alongside it rather than assumed.
func TestRecoveryNeedsMatchingParams(t *testing.T) {
	p := cheapParams(t)
	vk, _ := GenerateVaultKey()
	blob, err := WrapVaultKeyPassphrase("pass", vk, p)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	altered := p
	altered.Time = p.Time + 1
	if _, err := UnwrapVaultKeyPassphrase("pass", blob, altered); err == nil {
		t.Error("recovered with different Argon2id cost than was used to wrap")
	}
	altered = p
	altered.Salt = append([]byte{}, p.Salt...)
	altered.Salt[0] ^= 0xff
	if _, err := UnwrapVaultKeyPassphrase("pass", blob, altered); err == nil {
		t.Error("recovered with a different salt")
	}
}

func TestRecoveryParamsAreDistinctPerCall(t *testing.T) {
	a, err := DefaultRecoveryParams()
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	b, err := DefaultRecoveryParams()
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	if bytes.Equal(a.Salt, b.Salt) {
		t.Fatal("two recovery params shared a salt")
	}
	if len(a.Salt) < 16 {
		t.Errorf("salt is %d bytes, want at least 16", len(a.Salt))
	}
}

func TestRecoveryRejectsIncompleteParams(t *testing.T) {
	vk, _ := GenerateVaultKey()
	bad := []RecoveryParams{
		{Time: 1, MemoryK: 1024, Threads: 1, Salt: []byte("short")},
		{Time: 0, MemoryK: 1024, Threads: 1, Salt: make([]byte, 16)},
		{Time: 1, MemoryK: 0, Threads: 1, Salt: make([]byte, 16)},
		{Time: 1, MemoryK: 1024, Threads: 0, Salt: make([]byte, 16)},
	}
	for _, p := range bad {
		if _, err := WrapVaultKeyPassphrase("pass", vk, p); err == nil {
			t.Errorf("wrapped with incomplete params %+v", p)
		}
	}
}

func TestWrapRejectsWrongSizedVaultKey(t *testing.T) {
	_, _, shared := pairFor(t)
	p := cheapParams(t)
	for _, k := range [][]byte{nil, make([]byte, 16), make([]byte, 64)} {
		if _, err := WrapVaultKey(shared, k); err == nil {
			t.Errorf("wrapped a %d-byte vault key", len(k))
		}
		if _, err := WrapVaultKeyPassphrase("pass", k, p); err == nil {
			t.Errorf("passphrase-wrapped a %d-byte vault key", len(k))
		}
	}
}

func TestWrapRejectsEmptyPassphrase(t *testing.T) {
	p := cheapParams(t)
	vk, _ := GenerateVaultKey()
	if _, err := WrapVaultKeyPassphrase("", vk, p); err == nil {
		t.Fatal("wrapped a vault key under an empty passphrase")
	}
}
