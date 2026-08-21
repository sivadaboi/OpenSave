package e2ee

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// A vault key is the symmetric key every device in one user's set shares.
//
// It exists because the pairwise keys SharedKey derives cannot serve a server
// that stores anything. A payload sealed with the A-to-B key is unreadable by
// C, so a store-and-forward server holding one copy has no ciphertext it can
// hand to every device. One key per vault, wrapped for each device under the
// pairwise key it already has, gives the server a single opaque blob and
// leaves it unable to read any of them.
//
// The pairwise keys do not go away: they are what a vault key is wrapped
// under when a device joins, and they remain what an out-of-band fingerprint
// check authenticates.
const VaultKeySize = 32

// Domain separation for every distinct use of a key here. A key minted or
// used for one purpose must never verify for another, even when the same
// bytes are involved.
const (
	vaultWrapInfo       = hkdfInfo + "/vault-wrap"
	vaultRecoveryInfo   = hkdfInfo + "/vault-recovery"
	vaultPayloadContext = hkdfInfo + "/vault-payload"
)

// sealAAD is Seal with associated data bound in. Seal itself delegates here
// with no AAD, so there is exactly one place that handles a nonce.
func sealAAD(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	// The nonce is prepended, so the ciphertext is self-contained.
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

// openAAD reverses sealAAD.
func openAAD(key, sealed, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("sealed payload is too short to contain a nonce")
	}
	nonce, body := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, body, aad)
	if err != nil {
		return nil, errors.New("payload failed its integrity check")
	}
	return plain, nil
}

// GenerateVaultKey mints a new vault key. Called once, by the first device in
// a vault; every other device receives this same key rather than minting one.
func GenerateVaultKey() ([]byte, error) {
	key := make([]byte, VaultKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate vault key: %w", err)
	}
	return key, nil
}

// WrapVaultKey seals a vault key for one peer, under the pairwise key the two
// devices already share. This is what travels when a device joins a vault.
//
// The wrap is domain-separated from ordinary payload sealing, so a wrapped
// key can never be opened by code expecting a save, or the reverse.
func WrapVaultKey(pairwise, vaultKey []byte) ([]byte, error) {
	if len(vaultKey) != VaultKeySize {
		return nil, fmt.Errorf("vault key must be %d bytes, got %d", VaultKeySize, len(vaultKey))
	}
	return sealAAD(pairwise, vaultKey, []byte(vaultWrapInfo))
}

// UnwrapVaultKey reverses WrapVaultKey.
func UnwrapVaultKey(pairwise, wrapped []byte) ([]byte, error) {
	key, err := openAAD(pairwise, wrapped, []byte(vaultWrapInfo))
	if err != nil {
		return nil, errors.New("could not unwrap vault key — wrong pairing key, or the wrap was altered")
	}
	if len(key) != VaultKeySize {
		return nil, errors.New("unwrapped vault key has the wrong length")
	}
	return key, nil
}

// Context names what a sealed payload is, and is bound into the ciphertext so
// it cannot be reinterpreted as anything else.
//
// This is the guarantee a stateless relay never needed. A server that stores
// blobs can also serve the wrong one: answer a request for game A with the
// blob from game B, or replay version 3 in place of version 7. Both decrypt
// perfectly well against a vault key, because a vault key says nothing about
// which save it protects. Binding the identity into the AEAD's associated
// data is what makes those substitutions fail closed.
type Context struct {
	VaultID string
	GameID  string
	Version uint64
}

// aad renders a Context into associated data.
//
// Fields are length-prefixed rather than joined: concatenating them directly
// would let ("ab","c") and ("a","bc") produce identical bytes, which is
// exactly the ambiguity that lets one identity masquerade as another.
func (c Context) aad() []byte {
	out := make([]byte, 0, len(vaultPayloadContext)+len(c.VaultID)+len(c.GameID)+32)
	appendField := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		out = append(out, n[:]...)
		out = append(out, s...)
	}
	appendField(vaultPayloadContext)
	appendField(c.VaultID)
	appendField(c.GameID)
	var v [8]byte
	binary.BigEndian.PutUint64(v[:], c.Version)
	return append(out, v[:]...)
}

// SealFor encrypts a payload under the vault key, bound to the identity in
// ctx. Only an OpenFor with an identical Context can open it.
func SealFor(vaultKey, plaintext []byte, ctx Context) ([]byte, error) {
	if ctx.VaultID == "" || ctx.GameID == "" {
		return nil, errors.New("seal context needs both a vault and a game id")
	}
	return sealAAD(vaultKey, plaintext, ctx.aad())
}

// OpenFor reverses SealFor. A mismatch in any Context field fails here rather
// than returning someone else's save.
func OpenFor(vaultKey, sealed []byte, ctx Context) ([]byte, error) {
	if ctx.VaultID == "" || ctx.GameID == "" {
		return nil, errors.New("open context needs both a vault and a game id")
	}
	plain, err := openAAD(vaultKey, sealed, ctx.aad())
	if err != nil {
		return nil, errors.New(
			"payload failed its integrity check — wrong key, altered, or served for a different save or version")
	}
	return plain, nil
}

// RecoveryParams records the Argon2id cost used to derive a recovery key, so
// a blob sealed today can still be opened after the defaults are raised.
type RecoveryParams struct {
	Time    uint32 `json:"time"`
	MemoryK uint32 `json:"memoryKiB"`
	Threads uint8  `json:"threads"`
	Salt    []byte `json:"salt"`
}

// DefaultRecoveryParams are the Argon2id costs used for new recovery blobs.
//
// 64 MiB stays comfortable on the low-memory handhelds this runs on while
// remaining expensive to attack in bulk. A passphrase is the one secret here
// a person can pick badly, so the KDF is what has to make a weak choice cost
// something.
func DefaultRecoveryParams() (RecoveryParams, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return RecoveryParams{}, fmt.Errorf("generate recovery salt: %w", err)
	}
	return RecoveryParams{Time: 3, MemoryK: 64 * 1024, Threads: 4, Salt: salt}, nil
}

// recoveryKey derives the wrapping key for a recovery blob.
func recoveryKey(passphrase string, p RecoveryParams) ([]byte, error) {
	if len(p.Salt) < 8 {
		return nil, errors.New("recovery salt is missing or too short")
	}
	if p.Time == 0 || p.MemoryK == 0 || p.Threads == 0 {
		return nil, errors.New("recovery parameters are incomplete")
	}
	return argon2.IDKey([]byte(passphrase), p.Salt, p.Time, p.MemoryK, p.Threads, VaultKeySize), nil
}

// WrapVaultKeyPassphrase seals a vault key under a key derived from a
// user-chosen passphrase.
//
// This is what makes a storing server usable on its own. Wrapping the vault
// key only for peers means a new device can join only while another device is
// online — precisely the situation the server was added to fix. A recovery
// blob lets a new device bootstrap from the server alone, using something the
// user knows.
func WrapVaultKeyPassphrase(passphrase string, vaultKey []byte, p RecoveryParams) ([]byte, error) {
	if len(vaultKey) != VaultKeySize {
		return nil, fmt.Errorf("vault key must be %d bytes, got %d", VaultKeySize, len(vaultKey))
	}
	if passphrase == "" {
		return nil, errors.New("recovery passphrase must not be empty")
	}
	wrapKey, err := recoveryKey(passphrase, p)
	if err != nil {
		return nil, err
	}
	return sealAAD(wrapKey, vaultKey, []byte(vaultRecoveryInfo))
}

// UnwrapVaultKeyPassphrase reverses WrapVaultKeyPassphrase. The same params
// that produced the blob must be supplied alongside it.
func UnwrapVaultKeyPassphrase(passphrase string, wrapped []byte, p RecoveryParams) ([]byte, error) {
	wrapKey, err := recoveryKey(passphrase, p)
	if err != nil {
		return nil, err
	}
	key, err := openAAD(wrapKey, wrapped, []byte(vaultRecoveryInfo))
	if err != nil {
		return nil, errors.New("recovery failed — wrong passphrase, or the recovery data was altered")
	}
	if len(key) != VaultKeySize {
		return nil, errors.New("recovered vault key has the wrong length")
	}
	return key, nil
}
