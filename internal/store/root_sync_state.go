package store

import "fmt"

// Merge-base lineage, per save location.
//
// Every function here dispatches on the root name: the primary location ("")
// reads and writes game_peer_sync_state exactly as it always has, so the
// lineage rules that took several rounds of bug-fixing to get right run over
// untouched storage. Extra locations use game_root_sync_state, which starts
// empty and has no history to preserve.
//
// Callers in the sync engine should use these rather than the two-argument
// forms, so one code path serves every location.

// GetAgreedHashForRoot returns the last-convergence hash for one location.
func (s *Store) GetAgreedHashForRoot(gameID, peerID, root string) string {
	if root == "" {
		return s.GetAgreedHash(gameID, peerID)
	}
	var hash string
	if err := s.db.Get(&hash,
		`SELECT agreed_hash FROM game_root_sync_state WHERE game_id = ? AND peer_id = ? AND root = ?`,
		gameID, peerID, root); err != nil {
		return ""
	}
	return hash
}

// SetAgreedHashForRoot records a convergence for one location, clearing that
// location's outstanding pushed hash for the reason SetAgreedHash explains.
func (s *Store) SetAgreedHashForRoot(gameID, peerID, root, hash string) error {
	if root == "" {
		return s.SetAgreedHash(gameID, peerID, hash)
	}
	_, err := s.db.Exec(`
		INSERT INTO game_root_sync_state (game_id, peer_id, root, agreed_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(game_id, peer_id, root) DO UPDATE SET
			agreed_hash = excluded.agreed_hash,
			pushed_hash = ''`,
		gameID, peerID, root, hash)
	if err != nil {
		return fmt.Errorf("set agreed hash %s/%s/%s: %w", gameID, peerID, root, err)
	}
	return nil
}

// GetPushedHashForRoot returns the state last handed to a peer for one
// location.
func (s *Store) GetPushedHashForRoot(gameID, peerID, root string) string {
	if root == "" {
		return s.GetPushedHash(gameID, peerID)
	}
	var hash string
	if err := s.db.Get(&hash,
		`SELECT pushed_hash FROM game_root_sync_state WHERE game_id = ? AND peer_id = ? AND root = ?`,
		gameID, peerID, root); err != nil {
		return ""
	}
	return hash
}

// SetPushedHashForRoot records what was just handed to a peer for one
// location.
func (s *Store) SetPushedHashForRoot(gameID, peerID, root, hash string) error {
	if root == "" {
		return s.SetPushedHash(gameID, peerID, hash)
	}
	_, err := s.db.Exec(`
		INSERT INTO game_root_sync_state (game_id, peer_id, root, pushed_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(game_id, peer_id, root) DO UPDATE SET pushed_hash = excluded.pushed_hash`,
		gameID, peerID, root, hash)
	if err != nil {
		return fmt.Errorf("set pushed hash %s/%s/%s: %w", gameID, peerID, root, err)
	}
	return nil
}

// ForgetRootSyncState drops a location's lineage with every peer. Called
// when a root is removed from a game: leaving the rows behind would mean a
// location re-added under the same name later inherits a merge base
// describing files that may be long gone.
func (s *Store) ForgetRootSyncState(gameID, root string) error {
	_, err := s.db.Exec(`DELETE FROM game_root_sync_state WHERE game_id = ? AND root = ?`,
		gameID, root)
	if err != nil {
		return fmt.Errorf("forget root sync state %s/%s: %w", gameID, root, err)
	}
	return nil
}
