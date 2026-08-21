package store

import (
	"fmt"

	"github.com/opensave/opensave/internal/e2ee"
)

// DeviceIdentity returns this device's long-lived X25519 key pair, generating
// and persisting it the first time it is asked for.
//
// Lazily rather than at install: a device that never pairs never needs one,
// and generating it on demand means an install upgrading into this gets a key
// without a migration having to invent one.
func (s *Store) DeviceIdentity() (e2ee.Identity, error) {
	settings, err := s.GetSettings()
	if err != nil {
		return e2ee.Identity{}, err
	}
	if settings.DevicePrivateKey != "" && settings.DevicePublicKey != "" {
		priv, err1 := e2ee.DecodeKey(settings.DevicePrivateKey)
		pub, err2 := e2ee.DecodeKey(settings.DevicePublicKey)
		if err1 == nil && err2 == nil {
			return e2ee.Identity{Private: priv, Public: pub}, nil
		}
		// Stored but unreadable. Replacing it is the only way forward, and it
		// invalidates every pairing — so say so rather than failing obscurely
		// later when a peer's key will not agree with ours.
		return e2ee.Identity{}, fmt.Errorf(
			"this device's encryption key is corrupt; re-pair your devices to generate a new one")
	}

	id, err := e2ee.GenerateIdentity()
	if err != nil {
		return e2ee.Identity{}, err
	}
	if _, err := s.db.Exec(
		`UPDATE settings SET device_private_key = ?, device_public_key = ? WHERE id = 1`,
		e2ee.EncodeKey(id.Private), e2ee.EncodeKey(id.Public)); err != nil {
		return e2ee.Identity{}, fmt.Errorf("store device key: %w", err)
	}
	return id, nil
}

// SetPeerPublicKey pins a peer's public half.
//
// Deliberately its own statement rather than a column on UpsertPeer. That is
// called from the ping loop and from several places that build a fresh Peer
// value, none of which know anything about keys — and any one of them would
// write an empty string over a pinned key, silently turning encryption off for
// that peer. A key should only ever be set by the code that has one.
func (s *Store) SetPeerPublicKey(peerID, publicKey string) error {
	if peerID == "" || publicKey == "" {
		return nil
	}
	if _, err := e2ee.DecodeKey(publicKey); err != nil {
		return fmt.Errorf("refusing to pin an unusable key for %s: %w", peerID, err)
	}
	if _, err := s.db.Exec(`UPDATE peers SET public_key = ? WHERE id = ?`, publicKey, peerID); err != nil {
		return fmt.Errorf("pin public key for %s: %w", peerID, err)
	}
	return nil
}

// SharedKeyWith derives the symmetric key this device shares with a peer.
//
// Returns ok=false, without an error, when there is simply no key to work
// with — a peer paired before this existed, or one running a build without it.
// That is the ordinary case during a rollout and means "send this in the
// clear", not "something went wrong".
func (s *Store) SharedKeyWith(peerID string) (key []byte, ok bool, err error) {
	peer, err := s.GetPeer(peerID)
	if err != nil {
		return nil, false, err
	}
	if peer.PublicKey == "" {
		return nil, false, nil
	}
	theirs, err := e2ee.DecodeKey(peer.PublicKey)
	if err != nil {
		return nil, false, fmt.Errorf("peer %s has an unusable public key: %w", peerID, err)
	}
	id, err := s.DeviceIdentity()
	if err != nil {
		return nil, false, err
	}
	shared, err := e2ee.SharedKey(id.Private, theirs)
	if err != nil {
		return nil, false, err
	}
	return shared, true, nil
}

// PairingFingerprint is the string both devices show for a pairing, for
// comparing out of band. Empty when there is no key pinned for the peer.
func (s *Store) PairingFingerprint(peerID string) (string, error) {
	peer, err := s.GetPeer(peerID)
	if err != nil {
		return "", err
	}
	if peer.PublicKey == "" {
		return "", nil
	}
	theirs, err := e2ee.DecodeKey(peer.PublicKey)
	if err != nil {
		return "", nil
	}
	id, err := s.DeviceIdentity()
	if err != nil {
		return "", err
	}
	return e2ee.Fingerprint(id.Public, theirs), nil
}
