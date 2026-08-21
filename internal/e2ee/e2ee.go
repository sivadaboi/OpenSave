// Package e2ee provides the key agreement and payload encryption that lets two
// paired devices exchange save data without the network — or a relay — being
// able to read it.
//
// The problem it solves: nothing in OpenSave encrypted a save. LAN sync is
// plain HTTP, so anything on the same network sees the bytes; relay sync is
// wss://, but that encryption terminates AT the relay, so whoever runs the
// relay sees them too. Both paths carry the save file itself.
//
// The shape here is deliberately small. Each device holds one long-lived
// X25519 key pair. Pairing exchanges the public halves, each side pins the
// other's, and the pair derives one shared key that neither the relay nor the
// network ever sees. Save payloads are sealed with it.
package e2ee

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// KeySize is the length of a raw X25519 key, public or private.
const KeySize = 32

// hkdfInfo domain-separates the derived key.
//
// Two devices could in principle share an X25519 secret for more than one
// purpose later; binding the derivation to a purpose string means a key minted
// here can never be mistaken for one minted elsewhere, even if the same pair
// of identities is involved.
const hkdfInfo = "opensave/e2ee/save-payload/v1"

// Identity is one device's long-lived key pair.
//
// Long-lived on purpose. The public half is pinned by every device this one
// pairs with, so rotating it means every pairing has to be redone — which is
// exactly the friction that makes an unexpected change worth noticing.
type Identity struct {
	Private []byte
	Public  []byte
}

// GenerateIdentity creates a new device key pair.
func GenerateIdentity() (Identity, error) {
	priv := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, priv); err != nil {
		return Identity{}, fmt.Errorf("generate device key: %w", err)
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return Identity{}, fmt.Errorf("derive device public key: %w", err)
	}
	return Identity{Private: priv, Public: pub}, nil
}

// EncodeKey renders a key for storage or for sending to a peer.
func EncodeKey(k []byte) string { return base64.StdEncoding.EncodeToString(k) }

// DecodeKey parses a key produced by EncodeKey, rejecting anything that is not
// a plausible X25519 key rather than letting a short or corrupt one through to
// the curve operation.
func DecodeKey(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("key is not valid base64: %w", err)
	}
	if len(raw) != KeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(raw), KeySize)
	}
	return raw, nil
}

// SharedKey derives the symmetric key two paired devices share.
//
// Both sides compute the same value from opposite halves — this device's
// private key with the peer's public one — and it never crosses the network.
// The raw X25519 output is run through HKDF rather than used directly: the
// curve output is not uniformly distributed, and a hash step is what turns it
// into something safe to use as a cipher key.
func SharedKey(myPrivate, theirPublic []byte) ([]byte, error) {
	if len(myPrivate) != KeySize || len(theirPublic) != KeySize {
		return nil, errors.New("both keys must be 32 bytes")
	}
	secret, err := curve25519.X25519(myPrivate, theirPublic)
	if err != nil {
		// Raised for the low-order points that would produce an all-zero
		// shared secret — a peer sending one is either broken or probing.
		return nil, fmt.Errorf("key agreement failed: %w", err)
	}
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, nil, []byte(hkdfInfo)), key); err != nil {
		return nil, fmt.Errorf("derive shared key: %w", err)
	}
	return key, nil
}

// Seal encrypts plaintext so only the holder of the same shared key can read
// it. The nonce is random and travels with the ciphertext.
//
// XChaCha20-Poly1305 rather than AES-GCM for its 24-byte nonce: at that size a
// random nonce will not repeat in any realistic number of messages, so there is
// no counter to keep, persist across restarts, or get wrong. A repeated nonce
// with the same key is the one mistake that breaks this construction outright.
func Seal(key, plaintext []byte) ([]byte, error) {
	return sealAAD(key, plaintext, nil)
}

// Open reverses Seal. It fails if the data was altered in any way, which is
// the point: a relay that rewrites a byte is caught here rather than landing a
// corrupted save on disk.
func Open(key, sealed []byte) ([]byte, error) {
	plain, err := openAAD(key, sealed, nil)
	if err != nil {
		// Keep this path's own wording: for a pairwise payload the cause is
		// always one of these two, with no server in between to blame.
		if strings.Contains(err.Error(), "integrity check") {
			return nil, errors.New("payload failed its integrity check — wrong key, or altered in transit")
		}
		return nil, err
	}
	return plain, nil
}

// Fingerprint is a short human-comparable digest of a pairing, for reading
// aloud or eyeballing on two screens.
//
// Pinning a peer's key on first sight is only as good as the first sight: a
// relay that substituted its own key at that moment would sit in the middle and
// neither side would know. Comparing this string out of band is what closes
// that, so it has to be the same on both devices — hence sorting the two keys
// before hashing, since each side holds them in the opposite order.
//
// Rendered as six groups of four hex characters: enough to compare in a few
// seconds, and far too much to collide with by accident.
func Fingerprint(pubA, pubB []byte) string {
	first, second := pubA, pubB
	for i := 0; i < len(pubA) && i < len(pubB); i++ {
		if pubA[i] != pubB[i] {
			if pubA[i] > pubB[i] {
				first, second = pubB, pubA
			}
			break
		}
	}
	sum := sha256.Sum256(append(append([]byte(hkdfInfo+"/fingerprint"), first...), second...))
	h := hex.EncodeToString(sum[:12])
	var groups []string
	for i := 0; i < len(h); i += 4 {
		groups = append(groups, h[i:i+4])
	}
	return strings.Join(groups, " ")
}
