package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Snapshot is one versioned backup of a game's save folder/file at a point
// in time, on a specific branch.
type Snapshot struct {
	ID           string `db:"id" json:"id"`
	GameID       string `db:"game_id" json:"gameId"`
	BranchName   string `db:"branch_name" json:"branch"`
	Timestamp    string `db:"timestamp" json:"timestamp"`
	Comment      string `db:"comment" json:"comment"`
	IsSystemAuto bool   `db:"is_system_auto" json:"isSystemAuto"`
	ZipPath      string `db:"zip_path" json:"zipPath"`
	SizeBytes    int64  `db:"size_bytes" json:"sizeBytes"`
}

// CreateSnapshot inserts a new snapshot record.
func (s *Store) CreateSnapshot(snap Snapshot) error {
	_, err := s.db.NamedExec(`
		INSERT INTO snapshots (id, game_id, branch_name, timestamp, comment, is_system_auto, zip_path, size_bytes)
		VALUES (:id, :game_id, :branch_name, :timestamp, :comment, :is_system_auto, :zip_path, :size_bytes)`,
		snap)
	if err != nil {
		return fmt.Errorf("create snapshot %s: %w", snap.ID, err)
	}
	return nil
}

// GetSnapshot returns a single snapshot by ID.
func (s *Store) GetSnapshot(id string) (Snapshot, error) {
	var snap Snapshot
	err := s.db.Get(&snap, `SELECT * FROM snapshots WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("get snapshot %s: %w", id, err)
	}
	return snap, nil
}

// ListSnapshots returns every snapshot for a game+branch, newest first.
func (s *Store) ListSnapshots(gameID, branchName string) ([]Snapshot, error) {
	var snaps []Snapshot
	err := s.db.Select(&snaps,
		`SELECT * FROM snapshots WHERE game_id = ? AND branch_name = ? ORDER BY timestamp DESC`,
		gameID, branchName)
	if err != nil {
		return nil, fmt.Errorf("list snapshots for %s/%s: %w", gameID, branchName, err)
	}
	return snaps, nil
}

// DeleteSnapshot removes a snapshot's metadata row. Callers must remove the
// underlying zip_path file themselves (see the same convention as
// DeleteGame).
func (s *Store) DeleteSnapshot(id string) error {
	res, err := s.db.Exec(`DELETE FROM snapshots WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete snapshot %s: %w", id, err)
	}
	return checkRowAffected(res)
}

// SnapshotsBeyondRetentionByKind returns the snapshots to prune when
// automatic and manual snapshots are budgeted separately.
//
// A single shared budget lets a game that auto-saves every few minutes push
// out snapshots the user took on purpose — the automatic ones are newer, so
// "keep the newest N" always favours them, and the manual snapshot is gone
// precisely when it is wanted. Counting the two kinds independently means a
// burst of auto-saves can only ever evict other auto-saves.
//
// Either limit at 0 or below keeps that kind entirely, matching the
// convention used by the per-game limit elsewhere.
func (s *Store) SnapshotsBeyondRetentionByKind(gameID, branchName string, maxAuto, maxManual int) ([]Snapshot, error) {
	all, err := s.ListSnapshots(gameID, branchName) // newest first
	if err != nil {
		return nil, err
	}
	var auto, manual []Snapshot
	for _, snap := range all {
		if snap.IsSystemAuto {
			auto = append(auto, snap)
		} else {
			manual = append(manual, snap)
		}
	}

	var beyond []Snapshot
	if maxAuto > 0 && len(auto) > maxAuto {
		beyond = append(beyond, auto[maxAuto:]...)
	}
	if maxManual > 0 && len(manual) > maxManual {
		beyond = append(beyond, manual[maxManual:]...)
	}
	return beyond, nil
}
