package e2ee

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// A vault holds a keyring rather than a single key, because a vault key can
// be replaced but the data sealed under it must never become unreadable.
//
// Two situations force a new key, and neither may cost anyone a save:
//
//   - Two devices that each already have a vault pair up. One key has to seal
//     what comes next, but both sides already have history sealed under
//     theirs.
//   - A device is unpaired and the key it holds should stop being useful for
//     anything sealed afterwards.
//
// Both are handled by adding an epoch and moving the current pointer, never
// by discarding. A blob records the KeyID that sealed it, so opening is a
// lookup rather than a guess, and a keyring that has accumulated five epochs
// still reads everything it ever sealed.
//
// Re-encrypting history would be the alternative, and it is worse: it needs
// every blob present and every device online at once, and a failure halfway
// through leaves saves no key can open.

// keyIDInfo domain-separates the key identifier from the key itself.
const keyIDInfo = hkdfInfo + "/vault-key-id"

// KeyID derives a short, stable identifier for a vault key.
//
// Derived rather than random so two devices that hold the same key agree on
// its name without having to exchange one, and hashed with its own domain
// string so publishing an ID — which travels beside every blob, in the clear
// — reveals nothing about the key.
func KeyID(vaultKey []byte) (string, error) {
	if len(vaultKey) != VaultKeySize {
		return "", fmt.Errorf("vault key must be %d bytes, got %d", VaultKeySize, len(vaultKey))
	}
	sum := sha256.Sum256(append([]byte(keyIDInfo), vaultKey...))
	return hex.EncodeToString(sum[:8]), nil
}

// KeyEpoch is one vault key and when this device learned of it.
type KeyEpoch struct {
	ID      string
	Key     []byte
	Created time.Time
}

// Keyring is every vault key a device holds, with one marked current.
//
// The zero value is not usable; build one with NewKeyring.
type Keyring struct {
	epochs  map[string]KeyEpoch
	current string
}

// NewKeyring starts a keyring from one key, which becomes current.
func NewKeyring(key []byte, created time.Time) (*Keyring, error) {
	id, err := KeyID(key)
	if err != nil {
		return nil, err
	}
	return &Keyring{
		epochs:  map[string]KeyEpoch{id: {ID: id, Key: append([]byte{}, key...), Created: created}},
		current: id,
	}, nil
}

// Add records a key without changing which one is current. Re-adding a key
// already held keeps the earlier Created time: the first sighting is the one
// that orders epochs, and a later re-learn must not reorder history.
func (k *Keyring) Add(key []byte, created time.Time) (string, error) {
	id, err := KeyID(key)
	if err != nil {
		return "", err
	}
	if existing, ok := k.epochs[id]; ok {
		if created.Before(existing.Created) {
			existing.Created = created
			k.epochs[id] = existing
		}
		return id, nil
	}
	k.epochs[id] = KeyEpoch{ID: id, Key: append([]byte{}, key...), Created: created}
	return id, nil
}

// SetCurrent points new seals at an already-held key.
func (k *Keyring) SetCurrent(id string) error {
	if _, ok := k.epochs[id]; !ok {
		return fmt.Errorf("keyring does not hold key %s", id)
	}
	k.current = id
	return nil
}

// Current returns the epoch new payloads are sealed under.
func (k *Keyring) Current() (KeyEpoch, error) {
	e, ok := k.epochs[k.current]
	if !ok {
		return KeyEpoch{}, errors.New("keyring has no current key")
	}
	return e, nil
}

// Get returns one held epoch by ID.
func (k *Keyring) Get(id string) (KeyEpoch, bool) {
	e, ok := k.epochs[id]
	return e, ok
}

// Epochs returns every held key, oldest first, for persisting a keyring.
func (k *Keyring) Epochs() []KeyEpoch {
	out := make([]KeyEpoch, 0, len(k.epochs))
	for _, e := range k.epochs {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created.Equal(out[j].Created) {
			return out[i].ID < out[j].ID
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out
}

// Seal encrypts under the current key, returning the KeyID to store beside
// the blob. Without that ID a later Open has nothing to look up.
func (k *Keyring) Seal(plaintext []byte, ctx Context) (sealed []byte, keyID string, err error) {
	e, err := k.Current()
	if err != nil {
		return nil, "", err
	}
	sealed, err = SealFor(e.Key, plaintext, ctx)
	if err != nil {
		return nil, "", err
	}
	return sealed, e.ID, nil
}

// Open decrypts a blob using the key it names.
//
// ctx must be the context the blob was sealed with, rebuilt from what was
// stored beside it — not from this device's current vault. A merge can change
// which vault a device calls its own, and reconstructing the context from
// present state would make correctly-keyed history fail to open.
func (k *Keyring) Open(sealed []byte, ctx Context, keyID string) ([]byte, error) {
	e, ok := k.epochs[keyID]
	if !ok {
		return nil, fmt.Errorf(
			"this device does not hold the key %s that sealed this save — pair with a device that does, or use the recovery passphrase", keyID)
	}
	return OpenFor(e.Key, sealed, ctx)
}

// Merge folds another keyring into this one: the union of both sets of keys,
// with the older current key staying current.
//
// Union rather than choose, because either side may already have history
// sealed under its own key, and dropping a key is what would make those
// unreadable. Preferring the older current key means the longer-established
// vault keeps sealing, so the device with more history is not the one that
// has to move.
//
// Merge is deliberately total: it cannot fail partway and leave a keyring
// holding less than it started with.
func (k *Keyring) Merge(other *Keyring) error {
	if other == nil {
		return nil
	}
	mineCurrent, err := k.Current()
	if err != nil {
		return err
	}
	theirsCurrent, err := other.Current()
	if err != nil {
		return err
	}
	for _, e := range other.Epochs() {
		if _, err := k.Add(e.Key, e.Created); err != nil {
			return err
		}
	}
	// Older wins; identical timestamps fall back to the ID so both devices
	// independently reach the same answer.
	takeTheirs := theirsCurrent.Created.Before(mineCurrent.Created) ||
		(theirsCurrent.Created.Equal(mineCurrent.Created) && theirsCurrent.ID < mineCurrent.ID)
	if takeTheirs {
		return k.SetCurrent(theirsCurrent.ID)
	}
	return k.SetCurrent(mineCurrent.ID)
}

// Rotate mints a new key, adds it, and makes it current. Held keys stay held,
// so everything sealed before this moment is still readable by this device.
func (k *Keyring) Rotate(now time.Time) (KeyEpoch, error) {
	key, err := GenerateVaultKey()
	if err != nil {
		return KeyEpoch{}, err
	}
	id, err := k.Add(key, now)
	if err != nil {
		return KeyEpoch{}, err
	}
	if err := k.SetCurrent(id); err != nil {
		return KeyEpoch{}, err
	}
	return k.epochs[id], nil
}
