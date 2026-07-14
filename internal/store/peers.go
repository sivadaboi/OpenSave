package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// NullString wraps sql.NullString so it serializes on the wire as a plain
// string (or null), matching the JS daemon — the raw sql.NullString shape
// ({"String":…,"Valid":…}) reached the dashboard, where `new Date(object)`
// rendered "last synced Invalid Date".
type NullString struct {
	sql.NullString
}

// MarshalJSON emits the string value, or null when unset.
func (n NullString) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.String)
}

// UnmarshalJSON accepts a plain string or null.
func (n *NullString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		n.Valid = false
		n.String = ""
		return nil
	}
	if err := json.Unmarshal(b, &n.String); err != nil {
		return err
	}
	n.Valid = true
	return nil
}

// Peer is a paired remote device.
type Peer struct {
	ID          string     `db:"id" json:"id"`
	Name        string     `db:"name" json:"name"`
	DeviceType  string     `db:"device_type" json:"deviceType"`
	Address     string     `db:"address" json:"address"`
	Port        int        `db:"port" json:"port"`
	PairedAt    string     `db:"paired_at" json:"pairedAt"`
	LastSynced  NullString `db:"last_synced" json:"lastSynced"`
	LastSeenMs  int64      `db:"last_seen_ms" json:"-"`
	Status      string     `db:"status" json:"status"`
}

// UpsertPeer inserts a new paired peer or updates an existing one's
// connection details (used both at pairing time and whenever a peer's
// address/status changes, e.g. LAN IP changed or WAN heartbeat received).
func (s *Store) UpsertPeer(p Peer) error {
	_, err := s.db.NamedExec(`
		INSERT INTO peers (id, name, device_type, address, port, status, last_seen_ms)
		VALUES (:id, :name, :device_type, :address, :port, :status, :last_seen_ms)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			device_type = excluded.device_type,
			address = excluded.address,
			port = excluded.port,
			status = excluded.status,
			last_seen_ms = excluded.last_seen_ms`,
		p)
	if err != nil {
		return fmt.Errorf("upsert peer %s: %w", p.ID, err)
	}
	return nil
}

// GetPeer returns a single paired peer by ID.
func (s *Store) GetPeer(id string) (Peer, error) {
	var p Peer
	err := s.db.Get(&p, `SELECT * FROM peers WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, ErrNotFound
	}
	if err != nil {
		return Peer{}, fmt.Errorf("get peer %s: %w", id, err)
	}
	return p, nil
}

// ListPeers returns every paired peer.
func (s *Store) ListPeers() ([]Peer, error) {
	var peers []Peer
	if err := s.db.Select(&peers, `SELECT * FROM peers ORDER BY name`); err != nil {
		return nil, fmt.Errorf("list peers: %w", err)
	}
	return peers, nil
}

// SetPeerPairedAt overrides a peer's paired_at timestamp (used by the
// legacy importer to carry over the original pairing date instead of the
// insert-time default).
func (s *Store) SetPeerPairedAt(id, timestampISO8601 string) error {
	res, err := s.db.Exec(`UPDATE peers SET paired_at = ? WHERE id = ?`, timestampISO8601, id)
	if err != nil {
		return fmt.Errorf("set peer paired_at %s: %w", id, err)
	}
	return checkRowAffected(res)
}

// UpdatePeerLastSynced stamps a peer's last_synced time (called after a
// successful sync run completes with that peer).
func (s *Store) UpdatePeerLastSynced(id, timestampISO8601 string) error {
	res, err := s.db.Exec(`UPDATE peers SET last_synced = ? WHERE id = ?`, timestampISO8601, id)
	if err != nil {
		return fmt.Errorf("update peer last_synced %s: %w", id, err)
	}
	return checkRowAffected(res)
}

// PrunePeersAtAddress removes paired peers registered at the same
// address:port under a DIFFERENT id, returning the names removed. Called
// when a pairing is confirmed: a fresh install on the same machine gets a
// new node ID, and without this the old identity lingers as a permanently
// offline ghost in the device list. Never applies to "relay" (all WAN
// peers share that pseudo-address).
func (s *Store) PrunePeersAtAddress(address string, port int, keepID string) ([]string, error) {
	if address == "" || address == "relay" {
		return nil, nil
	}
	var names []string
	if err := s.db.Select(&names,
		`SELECT name FROM peers WHERE address = ? AND port = ? AND id != ?`,
		address, port, keepID); err != nil {
		return nil, fmt.Errorf("find stale peers at %s:%d: %w", address, port, err)
	}
	if len(names) == 0 {
		return nil, nil
	}
	if _, err := s.db.Exec(
		`DELETE FROM game_peer_sync_state WHERE peer_id IN
		   (SELECT id FROM peers WHERE address = ? AND port = ? AND id != ?)`,
		address, port, keepID); err != nil {
		return nil, fmt.Errorf("prune stale peer sync state: %w", err)
	}
	if _, err := s.db.Exec(
		`DELETE FROM peers WHERE address = ? AND port = ? AND id != ?`,
		address, port, keepID); err != nil {
		return nil, fmt.Errorf("prune stale peers at %s:%d: %w", address, port, err)
	}
	return names, nil
}

// UnpairPeer removes a paired peer and its per-game sync lineage state.
func (s *Store) UnpairPeer(id string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM game_peer_sync_state WHERE peer_id = ?`, id); err != nil {
		return fmt.Errorf("delete sync state for peer %s: %w", id, err)
	}
	res, err := tx.Exec(`DELETE FROM peers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete peer %s: %w", id, err)
	}
	if err := checkRowAffected(res); err != nil {
		return err
	}
	return tx.Commit()
}

// GamePeerSyncState is the lineage bookkeeping used by the P2P sync engine
// to compute conflict/direction without relying on wall-clock comparisons:
// the set of relative file/dir paths that were part of the last
// successful sync with a given peer for a given game.
type GamePeerSyncState struct {
	GameID          string `db:"game_id"`
	PeerID          string `db:"peer_id"`
	LastSyncedFiles string `db:"last_synced_files"`
	LastSyncedDirs  string `db:"last_synced_dirs"`
}

// GetSyncState returns the last-synced file/dir path sets for a game+peer,
// or empty sets if this is the first sync ever attempted with that peer.
func (s *Store) GetSyncState(gameID, peerID string) (files, dirs []string, err error) {
	var row GamePeerSyncState
	dbErr := s.db.Get(&row, `SELECT * FROM game_peer_sync_state WHERE game_id = ? AND peer_id = ?`, gameID, peerID)
	if errors.Is(dbErr, sql.ErrNoRows) {
		return []string{}, []string{}, nil
	}
	if dbErr != nil {
		return nil, nil, fmt.Errorf("get sync state %s/%s: %w", gameID, peerID, dbErr)
	}
	if err := json.Unmarshal([]byte(row.LastSyncedFiles), &files); err != nil {
		return nil, nil, fmt.Errorf("unmarshal last_synced_files: %w", err)
	}
	if err := json.Unmarshal([]byte(row.LastSyncedDirs), &dirs); err != nil {
		return nil, nil, fmt.Errorf("unmarshal last_synced_dirs: %w", err)
	}
	return files, dirs, nil
}

// SetSyncState replaces the lineage bookkeeping for a game+peer wholesale
// after a successful sync — matches the JS app's Set-replace-in-full
// approach rather than incremental row-level tracking.
func (s *Store) SetSyncState(gameID, peerID string, files, dirs []string) error {
	if files == nil {
		files = []string{}
	}
	if dirs == nil {
		dirs = []string{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return err
	}
	dirsJSON, err := json.Marshal(dirs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO game_peer_sync_state (game_id, peer_id, last_synced_files, last_synced_dirs)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(game_id, peer_id) DO UPDATE SET
			last_synced_files = excluded.last_synced_files,
			last_synced_dirs = excluded.last_synced_dirs`,
		gameID, peerID, string(filesJSON), string(dirsJSON))
	if err != nil {
		return fmt.Errorf("set sync state %s/%s: %w", gameID, peerID, err)
	}
	return nil
}
