package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

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

// GetSyncStateForRoot returns the path sets both sides last held for one
// location. Empty sets mean this location has never synced with that peer,
// which is what makes a first sync a first sync rather than a divergence.
func (s *Store) GetSyncStateForRoot(gameID, peerID, root string) (files, dirs []string, err error) {
	if root == "" {
		return s.GetSyncState(gameID, peerID)
	}
	var row struct {
		LastSyncedFiles string `db:"last_synced_files"`
		LastSyncedDirs  string `db:"last_synced_dirs"`
	}
	dbErr := s.db.Get(&row,
		`SELECT last_synced_files, last_synced_dirs FROM game_root_sync_state
		 WHERE game_id = ? AND peer_id = ? AND root = ?`, gameID, peerID, root)
	if errors.Is(dbErr, sql.ErrNoRows) {
		return []string{}, []string{}, nil
	}
	if dbErr != nil {
		return nil, nil, fmt.Errorf("get root sync state %s/%s/%s: %w", gameID, peerID, root, dbErr)
	}
	if err := json.Unmarshal([]byte(row.LastSyncedFiles), &files); err != nil {
		return nil, nil, fmt.Errorf("unmarshal root last_synced_files: %w", err)
	}
	if err := json.Unmarshal([]byte(row.LastSyncedDirs), &dirs); err != nil {
		return nil, nil, fmt.Errorf("unmarshal root last_synced_dirs: %w", err)
	}
	return files, dirs, nil
}

// SetSyncStateForRoot replaces one location's lineage bookkeeping wholesale.
func (s *Store) SetSyncStateForRoot(gameID, peerID, root string, files, dirs []string) error {
	if root == "" {
		return s.SetSyncState(gameID, peerID, files, dirs)
	}
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
		INSERT INTO game_root_sync_state (game_id, peer_id, root, last_synced_files, last_synced_dirs)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(game_id, peer_id, root) DO UPDATE SET
			last_synced_files = excluded.last_synced_files,
			last_synced_dirs  = excluded.last_synced_dirs`,
		gameID, peerID, root, string(filesJSON), string(dirsJSON))
	if err != nil {
		return fmt.Errorf("set root sync state %s/%s/%s: %w", gameID, peerID, root, err)
	}
	return nil
}

// ClearAgreedHashesForGame forgets every peer's merge base for a game, in
// both the primary and per-location tables.
//
// Called when a game's exclusion rules change. A merge base is a hash of what
// both devices held, and changing the rules changes what "held" means — the
// stored value was computed over a save that included files now excluded, so
// neither side can ever match it again. Left in place it reads as permanent
// two-way divergence: a conflict on every sync, over files nobody is syncing.
//
// Dropping it costs one pass of re-establishing agreement, which the next
// sync does on its own. The lineage is deliberately kept: it still correctly
// records which paths the two devices share, and losing it would make every
// deletion look like a new file.
func (s *Store) RebaseAgreedHashesForGame(gameID, newHash string) error {
	// Only rows that already held an agreement are rewritten. A peer this
	// game has never converged with must keep no base at all — inventing one
	// would claim an agreement that never happened.
	_, err := s.db.Exec(
		`UPDATE game_peer_sync_state SET agreed_hash = ?, pushed_hash = ''
		 WHERE game_id = ? AND agreed_hash != ''`, newHash, gameID)
	if err != nil {
		return fmt.Errorf("rebase agreed hashes for %s: %w", gameID, err)
	}
	// Extra locations are not affected by the primary rebase; their own bases
	// are cleared, since a rule can exclude files inside them too and there is
	// no single hash that fits every location.
	if _, err := s.db.Exec(
		`UPDATE game_root_sync_state SET agreed_hash = '', pushed_hash = '' WHERE game_id = ?`,
		gameID); err != nil {
		return fmt.Errorf("clear root agreed hashes for %s: %w", gameID, err)
	}
	return nil
}

func (s *Store) ClearAgreedHashesForGame(gameID string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE game_peer_sync_state SET agreed_hash = '', pushed_hash = '' WHERE game_id = ?`,
		gameID); err != nil {
		return fmt.Errorf("clear agreed hashes for %s: %w", gameID, err)
	}
	if _, err := tx.Exec(
		`UPDATE game_root_sync_state SET agreed_hash = '', pushed_hash = '' WHERE game_id = ?`,
		gameID); err != nil {
		return fmt.Errorf("clear root agreed hashes for %s: %w", gameID, err)
	}
	return tx.Commit()
}
