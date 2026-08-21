package store

import "fmt"

// CapturedFile is one file inside a snapshot: which save location it belongs
// to, its path within that location, and the hash of its contents.
//
// Root is empty for the game's primary save folder, matching delta.PrimaryRoot,
// so a game with a single folder reads exactly as it would have before extra
// locations existed.
type CapturedFile struct {
	Root string `db:"root"`
	Path string `db:"path"`
	Hash string `db:"hash"`
}

// RecordSnapshotFiles writes what a snapshot captured, one row per file.
//
// Written once, when the snapshot is created, and never updated afterwards.
// That is deliberate and is the whole difference between this and the
// whole-save hash it replaces: a snapshot is a fact about a moment, so nothing
// that happens later can make this description of it wrong. A value describing
// "the current state" is wrong the instant any code path forgets to move it,
// which is how a save with one edited file came to read as entirely
// uncaptured.
//
// A snapshot with no files is not an error — an empty save folder is a real
// thing, and is already reported elsewhere.
func (s *Store) RecordSnapshotFiles(snapshotID string, files []CapturedFile) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("record snapshot files for %s: %w", snapshotID, err)
	}
	defer tx.Rollback()

	// Prepared once: a save can hold thousands of files and this runs while
	// somebody is waiting for a snapshot to finish.
	stmt, err := tx.Preparex(
		`INSERT OR REPLACE INTO snapshot_files (snapshot_id, root, path, hash) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("record snapshot files for %s: %w", snapshotID, err)
	}
	defer stmt.Close()

	for _, f := range files {
		if _, err := stmt.Exec(snapshotID, f.Root, f.Path, f.Hash); err != nil {
			return fmt.Errorf("record snapshot file %q for %s: %w", f.Path, snapshotID, err)
		}
	}
	return tx.Commit()
}

// IsContentCaptured reports whether some snapshot of this game holds exactly
// this content, for exactly this path, in exactly this location.
//
// False means "cannot be shown to be captured", NOT "is not captured".
// Snapshots taken before these records existed have no rows and answer false
// for everything they hold. Callers must therefore treat false as the cautious
// answer and protect the file anyway: doing so needlessly costs a snapshot,
// while reading false as proof of absence would skip protecting a file that
// was never safe, and that is how saves are lost.
func (s *Store) IsContentCaptured(gameID, root, path, hash string) (bool, error) {
	if gameID == "" || path == "" || hash == "" {
		return false, nil
	}
	var n int
	err := s.db.Get(&n, `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM snapshot_files sf
			JOIN snapshots s ON s.id = sf.snapshot_id
			WHERE s.game_id = ? AND sf.root = ? AND sf.path = ? AND sf.hash = ?
			LIMIT 1
		)`, gameID, root, path, hash)
	if err != nil {
		return false, fmt.Errorf("look up captured content for %s/%s: %w", gameID, path, err)
	}
	return n > 0, nil
}

// SnapshotFiles returns what a single snapshot captured. Empty for a snapshot
// taken before these records existed, which is not distinguishable from — and
// deliberately treated the same as — a snapshot of nothing.
func (s *Store) SnapshotFiles(snapshotID string) ([]CapturedFile, error) {
	var rows []CapturedFile
	if err := s.db.Select(&rows,
		`SELECT root, path, hash FROM snapshot_files WHERE snapshot_id = ? ORDER BY root, path`,
		snapshotID); err != nil {
		return nil, fmt.Errorf("list snapshot files for %s: %w", snapshotID, err)
	}
	return rows, nil
}
