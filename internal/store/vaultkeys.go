package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/opensave/opensave/internal/e2ee"
)

// ErrNoVault means this device has not joined or created a vault yet.
var ErrNoVault = errors.New("no vault on this device")

// vaultKeyRow is one persisted epoch.
type vaultKeyRow struct {
	KeyID     string `db:"key_id"`
	KeyB64    string `db:"key_b64"`
	CreatedAt string `db:"created_at"`
}

// LoadKeyring reads this device's vault keyring.
//
// Returns ErrNoVault when there is none, which is the normal state for a
// device that has never paired — callers decide whether to create one rather
// than having one appear as a side effect of asking.
func (s *Store) LoadKeyring() (*e2ee.Keyring, error) {
	settings, err := s.GetSettings()
	if err != nil {
		return nil, err
	}
	if settings.VaultCurrentKeyID == "" {
		return nil, ErrNoVault
	}

	var rows []vaultKeyRow
	if err := s.db.Select(&rows, `SELECT * FROM vault_keys ORDER BY created_at, key_id`); err != nil {
		return nil, fmt.Errorf("list vault keys: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNoVault
	}

	var kr *e2ee.Keyring
	for _, r := range rows {
		key, err := base64.StdEncoding.DecodeString(r.KeyB64)
		if err != nil {
			return nil, fmt.Errorf("vault key %s is corrupt: %w", r.KeyID, err)
		}
		created, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if err != nil {
			// An unparseable timestamp only affects epoch ordering, and
			// refusing to load the whole keyring over it would strand every
			// save the other keys can still open.
			created = time.Unix(0, 0).UTC()
		}
		if kr == nil {
			kr, err = e2ee.NewKeyring(key, created)
			if err != nil {
				return nil, fmt.Errorf("load vault key %s: %w", r.KeyID, err)
			}
			continue
		}
		if _, err := kr.Add(key, created); err != nil {
			return nil, fmt.Errorf("load vault key %s: %w", r.KeyID, err)
		}
	}
	if err := kr.SetCurrent(settings.VaultCurrentKeyID); err != nil {
		// The current pointer names a key this device does not hold. Every
		// stored key still opens what it sealed, so the recoverable move is
		// to seal new data under the newest key held rather than refuse to
		// open anything.
		epochs := kr.Epochs()
		newest := epochs[len(epochs)-1]
		if err := kr.SetCurrent(newest.ID); err != nil {
			return nil, fmt.Errorf("vault keyring has no usable current key: %w", err)
		}
	}
	return kr, nil
}

// SaveKeyring persists a keyring: every epoch it holds, plus which one is
// current.
//
// Append-only and transactional. Existing rows are never deleted or rewritten,
// because a key this device drops is a save this device can no longer open —
// and a half-applied write is exactly how that would happen without the
// transaction.
func (s *Store) SaveKeyring(kr *e2ee.Keyring) error {
	if kr == nil {
		return errors.New("save keyring: nil keyring")
	}
	current, err := kr.Current()
	if err != nil {
		return fmt.Errorf("save keyring: %w", err)
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin vault key tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, e := range kr.Epochs() {
		// INSERT OR IGNORE, never UPDATE: a row already here is already
		// correct, and rewriting it could only lose the original Created.
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO vault_keys (key_id, key_b64, created_at) VALUES (?, ?, ?)`,
			e.ID, base64.StdEncoding.EncodeToString(e.Key), e.Created.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("store vault key %s: %w", e.ID, err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE settings SET vault_current_key_id = ? WHERE id = 1`, current.ID); err != nil {
		return fmt.Errorf("set current vault key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vault keys: %w", err)
	}
	return nil
}

// VaultID returns the vault this device belongs to, or "" if none.
func (s *Store) VaultID() (string, error) {
	settings, err := s.GetSettings()
	if err != nil {
		return "", err
	}
	return settings.VaultID, nil
}

// SetVaultID records which vault this device belongs to.
//
// Changing it does not change how existing blobs are opened: the vault id that
// seals a payload is stored beside that payload and is what must be replayed
// to open it. This column says where new data goes.
func (s *Store) SetVaultID(id string) error {
	if id == "" {
		return errors.New("vault id must not be empty")
	}
	if _, err := s.db.Exec(`UPDATE settings SET vault_id = ? WHERE id = 1`, id); err != nil {
		return fmt.Errorf("set vault id: %w", err)
	}
	return nil
}

// EnsureVault returns this device's vault, creating one the first time it is
// needed.
//
// Lazily, matching DeviceIdentity: a device that never syncs through a storing
// server never needs a vault, and creating one on demand means an install
// upgrading into this gets a working vault without a migration having to
// invent a key no other device has seen.
func (s *Store) EnsureVault() (*e2ee.Keyring, string, error) {
	kr, err := s.LoadKeyring()
	switch {
	case err == nil:
		id, err := s.VaultID()
		if err != nil {
			return nil, "", err
		}
		if id == "" {
			// Keys without an id: finish the job rather than mint a second
			// vault around the same keys.
			id, err = newVaultID()
			if err != nil {
				return nil, "", err
			}
			if err := s.SetVaultID(id); err != nil {
				return nil, "", err
			}
		}
		return kr, id, nil
	case errors.Is(err, ErrNoVault):
		// fall through and create
	default:
		return nil, "", err
	}

	key, err := e2ee.GenerateVaultKey()
	if err != nil {
		return nil, "", err
	}
	kr, err = e2ee.NewKeyring(key, time.Now().UTC())
	if err != nil {
		return nil, "", err
	}
	id, err := newVaultID()
	if err != nil {
		return nil, "", err
	}
	if err := s.SaveKeyring(kr); err != nil {
		return nil, "", err
	}
	if err := s.SetVaultID(id); err != nil {
		return nil, "", err
	}
	return kr, id, nil
}

// JoinVault adopts a vault learned from a peer: its id becomes this device's,
// and its keys are merged in rather than replacing what is already held.
//
// Merged, because this device may already have history sealed under its own
// key. Dropping that key is what would make those saves unreadable, so both
// sides keep everything and only the current pointer moves.
func (s *Store) JoinVault(vaultID string, theirs *e2ee.Keyring) (*e2ee.Keyring, error) {
	if vaultID == "" {
		return nil, errors.New("join vault: empty vault id")
	}
	if theirs == nil {
		return nil, errors.New("join vault: nil keyring")
	}
	mine, err := s.LoadKeyring()
	switch {
	case errors.Is(err, ErrNoVault):
		mine = theirs
	case err != nil:
		return nil, err
	default:
		if err := mine.Merge(theirs); err != nil {
			return nil, fmt.Errorf("merge vault keys: %w", err)
		}
	}
	if err := s.SaveKeyring(mine); err != nil {
		return nil, err
	}
	if err := s.SetVaultID(vaultID); err != nil {
		return nil, err
	}
	return mine, nil
}

// newVaultID mints an identifier for a vault. Random rather than derived from
// the key: it is bound into every sealed payload as associated data, and a
// value that changes when the key rotates would make old blobs unopenable.
func newVaultID() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate vault id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
