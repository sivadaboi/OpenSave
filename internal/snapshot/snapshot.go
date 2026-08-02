// Package snapshot implements ZIP-based save versioning and branch
// switching, porting src/daemon/snapshot.js: snapshots are ZIP archives
// (store method, no compression — saves are usually already compressed or
// small) of the save folder's contents (or the single save file), with
// per-branch history, retention pruning, and safety snapshots before any
// destructive restore.
package snapshot

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opensave/opensave/internal/store"
)

// UploadHook is called in the background after each snapshot is created,
// with the local zip path and the remote filename encoding game metadata
// (gameId__branch__snap_<ts>.zip). Wired to cloud backup in Phase 3; nil
// disables it.
type UploadHook func(zipPath, remoteFileName string)

// Manager performs snapshot/branch operations against the store and
// filesystem.
type Manager struct {
	Store    *store.Store
	OnUpload UploadHook
	// Log receives operational warnings (skipped unreadable files, …).
	// Optional; nil disables.
	Log func(level, msg string)
	// now is swappable for tests; defaults to time.Now.
	now func() time.Time
	// idMu serialises snapshot id selection against insertion; see
	// claimAndInsert.
	idMu sync.Mutex
	// stagingSeq names the in-progress archive uniquely, independently of
	// the clock.
	stagingSeq atomic.Uint64
}

// New creates a snapshot Manager.
func New(s *store.Store) *Manager {
	return &Manager{Store: s, now: time.Now}
}

// ParseExportEntryName splits a "gameId__branch__snapId.zip" entry name
// (the .sscb export / cloud upload naming scheme) into its parts.
func ParseExportEntryName(name string) (gameID, branch, snapID string, ok bool) {
	name = strings.TrimSuffix(name, ".zip")
	parts := strings.SplitN(name, "__", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

var branchNameRe = regexp.MustCompile(`[^a-z0-9_-]`)

// CleanBranchName sanitizes a user-supplied branch name the same way the
// JS app does: lowercase, restricted to [a-z0-9_-].
func CleanBranchName(name string) string {
	return branchNameRe.ReplaceAllString(toLower(name), "")
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// Create makes a new snapshot of the game's current save state on its
// active branch, prunes history beyond the game's maxSnapshots, and fires
// the background upload hook.
func (m *Manager) Create(gameID, comment string, isSystemAuto bool) (store.Snapshot, error) {
	game, err := m.Store.GetGame(gameID)
	if err != nil {
		return store.Snapshot{}, err
	}
	settings, err := m.Store.GetSettings()
	if err != nil {
		return store.Snapshot{}, err
	}

	if err := ensureSavePathExists(game.SavePath); err != nil {
		return store.Snapshot{}, err
	}

	backupDir := filepath.Join(settings.BackupsDir, gameID, game.ActiveBranch)
	if err := os.MkdirAll(backupDir, 0o777); err != nil {
		return store.Snapshot{}, fmt.Errorf("create backup dir: %w", err)
	}

	// Zip under a staging name, then claim an id and rename. Naming the zip
	// after the id up front would mean choosing the id before the slow part,
	// and the id cannot be chosen safely until it is about to be inserted.
	// Uniqueness must not come from m.now: that clock is swappable and tests
	// freeze it, which handed every concurrent snapshot the same staging name
	// and made them fight over one file. A counter cannot be frozen.
	stagingPath := filepath.Join(backupDir,
		fmt.Sprintf(".staging-%d-%d.zip", os.Getpid(), m.stagingSeq.Add(1)))
	skipped, err := ZipPath(game.SavePath, stagingPath)
	if err != nil {
		os.Remove(stagingPath)
		return store.Snapshot{}, fmt.Errorf("zip save data: %w", err)
	}
	defer os.Remove(stagingPath) // no-op once renamed
	if len(skipped) > 0 && m.Log != nil {
		sample := skipped[0]
		m.Log("warn", fmt.Sprintf("snapshot of %q skipped %d unreadable file(s), e.g. %s", game.Name, len(skipped), sample))
	}

	if comment == "" {
		if isSystemAuto {
			comment = "Auto backup"
		} else {
			comment = "Manual snapshot"
		}
	}

	snap, zipPath, err := m.claimAndInsert(gameID, game.ActiveBranch, backupDir, stagingPath, comment, isSystemAuto)
	if err != nil {
		return store.Snapshot{}, err
	}
	snapshotID := snap.ID

	m.pruneRetention(game)

	// A snapshot with no files inside is almost always a wrong tracked
	// path (native location tracked while the game plays through
	// Proton, dir emptied by an uninstall, ...). Keep it locally — it's
	// honest history — but say so loudly and never mirror it to the
	// cloud, where an "empty backup" destroys trust silently.
	if fileCount, cErr := zipFileCount(zipPath); cErr == nil && fileCount == 0 {
		if m.Log != nil {
			m.Log("warn", fmt.Sprintf(
				"snapshot of %q contains no files — check that the tracked save location (%s) is where the game actually saves; cloud mirror skipped",
				game.Name, game.SavePath))
		}
		return snap, nil
	}

	if m.OnUpload != nil {
		remoteName := fmt.Sprintf("%s__%s__%s.zip", gameID, game.ActiveBranch, snapshotID)
		go m.OnUpload(zipPath, remoteName)
	}

	return snap, nil
}

// zipFileCount returns the number of regular-file entries in a zip.
func zipFileCount(zipPath string) (int, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	n := 0
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			n++
		}
	}
	return n, nil
}

// claimAndInsert picks a free snapshot id, moves the staged zip into place
// under that name, and records it — as one operation, under a lock.
//
// Ids are snap_<unix-millis>, so two snapshots taken in the same millisecond
// want the same id. That is not hypothetical: a sync snapshotting several
// games at once, or the automatic backup before a rollback landing beside a
// manual one, does it on any reasonably fast machine. It surfaced as
// "UNIQUE constraint failed: snapshots.id" and the snapshot did not happen —
// a backup that silently did not get taken.
//
// Checking whether an id is free and then inserting it is not enough on its
// own: between the two, another goroutine claims the same id. The lock closes
// that window, and the insert is still retried on a collision so a second
// process (the CLI daemon alongside the app) cannot slip through either.
//
// Walking forward a millisecond keeps the id numeric, which snapIDToTimestamp
// relies on to read the time back out of it. Being a millisecond out is
// meaningless; not existing is not.
func (m *Manager) claimAndInsert(gameID, branch, backupDir, stagingPath, comment string, isSystemAuto bool) (store.Snapshot, string, error) {
	info, err := os.Stat(stagingPath)
	if err != nil {
		return store.Snapshot{}, "", err
	}
	size := info.Size()

	m.idMu.Lock()
	defer m.idMu.Unlock()

	ms := m.now().UnixMilli()
	for attempt := 0; attempt < 1000; attempt++ {
		id := "snap_" + strconv.FormatInt(ms, 10)
		if _, err := m.Store.GetSnapshot(id); err == nil {
			ms++
			continue // already taken
		}

		zipPath := filepath.Join(backupDir, id+".zip")
		if err := os.Rename(stagingPath, zipPath); err != nil {
			return store.Snapshot{}, "", fmt.Errorf("name snapshot archive: %w", err)
		}

		snap := store.Snapshot{
			ID:           id,
			GameID:       gameID,
			BranchName:   branch,
			Timestamp:    time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z"),
			Comment:      comment,
			IsSystemAuto: isSystemAuto,
			ZipPath:      zipPath,
			SizeBytes:    size,
		}
		if err := m.Store.CreateSnapshot(snap); err != nil {
			// Put the archive back under the staging name either way, so the
			// next attempt has something to move and a failure leaves no
			// half-named file behind.
			if renameErr := os.Rename(zipPath, stagingPath); renameErr != nil {
				os.Remove(zipPath)
				return store.Snapshot{}, "", err
			}
			// Only an id collision is worth walking forward for. Anything
			// else — a closed database, a full disk — will fail identically
			// on all 1000 attempts, and reporting it as "could not find a
			// free snapshot id" hides the actual cause behind a wrong one.
			if !isIDCollision(err) {
				return store.Snapshot{}, "", err
			}
			ms++
			continue
		}
		return snap, zipPath, nil
	}
	return store.Snapshot{}, "", fmt.Errorf("could not find a free snapshot id near %d", ms)
}

// isIDCollision reports whether an insert failed because the id was already
// taken, as opposed to any other database error. SQLite reports this as a
// uniqueness violation naming the column; the driver (modernc) surfaces it as
// a plain error, so the text is what there is to match on.
func isIDCollision(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// pruneRetention deletes the oldest snapshots (metadata + zip file,
// best-effort on the file) beyond the game's retention limits. A limit
// of 0 or below disables pruning for that kind.
//
// Retention applies to EVERY branch, not just the active one: conflict
// resolutions and manual branching create side branches whose snapshots
// would otherwise accumulate forever (a real disk-filler once a game has
// picked up a few conflict-* branches).
func (m *Manager) pruneRetention(game store.Game) {
	m.pruneGameAllBranches(game)
}

// pruneGameAllBranches deletes snapshots beyond the limits on every branch
// of a game and returns how many were removed and the bytes freed.
//
// Automatic and manual snapshots are budgeted separately (see
// SnapshotsBeyondRetentionByKind): a game that auto-saves constantly must not
// be able to evict the snapshots its user took deliberately.
func (m *Manager) pruneGameAllBranches(game store.Game) (removed int, freed int64) {
	// Nothing to do only when BOTH kinds are unlimited. Checking just the
	// automatic limit here would skip manual pruning entirely for the common
	// setup of "keep all auto-saves, keep 20 manual".
	if game.MaxSnapshots <= 0 && game.MaxManualSnapshots <= 0 {
		return 0, 0
	}
	branches, err := m.Store.ListBranches(game.ID)
	if err != nil {
		return 0, 0
	}
	for _, branch := range branches {
		beyond, err := m.Store.SnapshotsBeyondRetentionByKind(
			game.ID, branch, game.MaxSnapshots, game.MaxManualSnapshots)
		if err != nil {
			continue
		}
		for _, snap := range beyond {
			if err := m.Store.DeleteSnapshot(snap.ID); err != nil {
				continue
			}
			if info, statErr := os.Stat(snap.ZipPath); statErr == nil {
				freed += info.Size()
			}
			os.Remove(snap.ZipPath) // best-effort, same as the JS app
			removed++
		}
	}
	return removed, freed
}

// PruneAllGames applies each game's retention limit across all its
// branches immediately — the "clean up old snapshots now" action. It also
// sweeps abandoned conflict-* branches (non-active leftovers, chiefly from
// resolved "keep both" conflicts), whose snapshots the per-branch limit
// never touches because each branch stays under it. Returns the total
// snapshots removed and bytes freed.
func (m *Manager) PruneAllGames() (removed int, freed int64, err error) {
	games, err := m.Store.ListGames()
	if err != nil {
		return 0, 0, err
	}
	for _, game := range games {
		r, f := m.pruneGameAllBranches(game)
		removed += r
		freed += f

		branches, bErr := m.Store.ListBranches(game.ID)
		if bErr != nil {
			continue
		}
		for _, branch := range branches {
			if branch == game.ActiveBranch || !strings.HasPrefix(branch, "conflict-") {
				continue
			}
			r, f := m.DeleteBranch(game.ID, branch)
			removed += r
			freed += f
		}
	}
	return removed, freed, nil
}

// DeleteSnapshot removes one snapshot (metadata row + its zip file) for a
// game. Returns the bytes freed.
func (m *Manager) DeleteSnapshot(gameID, snapshotID string) (freed int64, err error) {
	snap, err := m.Store.GetSnapshot(snapshotID)
	if err != nil {
		return 0, err
	}
	if snap.GameID != gameID {
		return 0, fmt.Errorf("snapshot %q does not belong to game %q", snapshotID, gameID)
	}
	if info, statErr := os.Stat(snap.ZipPath); statErr == nil {
		freed = info.Size()
	}
	if err := m.Store.DeleteSnapshot(snapshotID); err != nil {
		return 0, err
	}
	os.Remove(snap.ZipPath) // best-effort
	return freed, nil
}

// DeleteBranch removes a branch entirely — every snapshot (metadata + zip)
// and the branch row. Refuses the game's active branch. Returns snapshots
// removed and bytes freed.
func (m *Manager) DeleteBranch(gameID, branch string) (removed int, freed int64) {
	game, err := m.Store.GetGame(gameID)
	if err != nil {
		return 0, 0
	}
	if branch == game.ActiveBranch {
		return 0, 0 // never delete the branch currently in play
	}
	snaps, err := m.Store.ListSnapshots(gameID, branch)
	if err != nil {
		return 0, 0
	}
	for _, snap := range snaps {
		if err := m.Store.DeleteSnapshot(snap.ID); err != nil {
			continue
		}
		if info, statErr := os.Stat(snap.ZipPath); statErr == nil {
			freed += info.Size()
		}
		os.Remove(snap.ZipPath)
		removed++
	}
	_ = m.Store.DeleteBranchRow(gameID, branch)
	return removed, freed
}

// Restore extracts the given snapshot over the game's save path, taking a
// safety snapshot of the current state first (when there is anything to
// save). The snapshot may live on any branch, matching the JS behavior of
// searching all branches.
func (m *Manager) Restore(gameID, snapshotID string) (store.Snapshot, error) {
	game, err := m.Store.GetGame(gameID)
	if err != nil {
		return store.Snapshot{}, err
	}
	snap, err := m.Store.GetSnapshot(snapshotID)
	if err != nil || snap.GameID != gameID {
		return store.Snapshot{}, fmt.Errorf("snapshot %q not found for game %q", snapshotID, gameID)
	}

	// The safety snapshot below triggers retention pruning, which — when the
	// game is at its snapshot limit and this is the oldest snapshot — would
	// delete this very snapshot's archive before we extract it. Restore from
	// a temporary copy so the content survives that pruning.
	restoreZip := snap.ZipPath
	if savePathHasContent(game.SavePath) {
		if tmp, err := copyToTempZip(snap.ZipPath); err == nil {
			restoreZip = tmp
			defer os.Remove(tmp)
		}
		safetyComment := fmt.Sprintf("Pre-rollback safety restore point (before restoring %s)", snapshotID)
		if _, err := m.Create(gameID, safetyComment, true); err != nil {
			// Non-fatal, same as JS: warn and continue the restore.
			fmt.Fprintf(os.Stderr, "[snapshot] safety snapshot before restore failed: %v\n", err)
		}
	}

	if err := UnzipTo(restoreZip, game.SavePath); err != nil {
		return store.Snapshot{}, fmt.Errorf("restore snapshot %s: %w", snapshotID, err)
	}
	return snap, nil
}

// copyToTempZip duplicates a snapshot archive to a temp file so a restore
// can read it even if retention deletes the original mid-operation.
func copyToTempZip(src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp("", "opensave-restore-*.zip")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// CreateBranch adds a new empty branch to a game.
func (m *Manager) CreateBranch(gameID, branchName string) (string, error) {
	if _, err := m.Store.GetGame(gameID); err != nil {
		return "", err
	}
	clean := CleanBranchName(branchName)
	if clean == "" {
		return "", errors.New("invalid branch name")
	}
	existing, err := m.Store.ListBranches(gameID)
	if err != nil {
		return "", err
	}
	for _, b := range existing {
		if b == clean {
			return "", fmt.Errorf("branch %q already exists", clean)
		}
	}
	if err := m.Store.CreateBranch(gameID, clean); err != nil {
		return "", err
	}
	return clean, nil
}

// SwitchBranch moves a game to another branch: auto-snapshot the current
// save state onto the outgoing branch, clear the save location, flip the
// active branch pointer, then restore the target branch's latest snapshot
// (if it has one — switching to a fresh branch leaves the save cleared).
func (m *Manager) SwitchBranch(gameID, targetBranch string) error {
	game, err := m.Store.GetGame(gameID)
	if err != nil {
		return err
	}
	if game.ActiveBranch == targetBranch {
		return nil
	}

	branches, err := m.Store.ListBranches(gameID)
	if err != nil {
		return err
	}
	found := false
	for _, b := range branches {
		if b == targetBranch {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("branch %q does not exist", targetBranch)
	}

	if savePathHasContent(game.SavePath) {
		comment := fmt.Sprintf("Auto backup before switching to branch %q", targetBranch)
		if _, err := m.Create(gameID, comment, true); err != nil {
			fmt.Fprintf(os.Stderr, "[snapshot] safety snapshot before branch switch failed: %v\n", err)
		}
	}

	if err := clearSavePath(game.SavePath); err != nil {
		return fmt.Errorf("clear save path: %w", err)
	}

	if err := m.Store.SwitchActiveBranch(gameID, targetBranch); err != nil {
		return err
	}

	targetSnaps, err := m.Store.ListSnapshots(gameID, targetBranch)
	if err != nil {
		return err
	}
	if len(targetSnaps) > 0 {
		latest := targetSnaps[0] // ListSnapshots returns newest first
		if err := UnzipTo(latest.ZipPath, game.SavePath); err != nil {
			// Same as JS: a failed restore of the incoming branch is logged
			// but the switch itself stands (branch pointer already moved).
			fmt.Fprintf(os.Stderr, "[snapshot] failed to restore branch snapshot: %v\n", err)
		}
	}
	return nil
}

// LatestSnapshot returns the most recent snapshot on a branch (or the
// game's active branch if branchName is empty), or ErrNotFound.
func (m *Manager) LatestSnapshot(gameID, branchName string) (store.Snapshot, error) {
	if branchName == "" {
		game, err := m.Store.GetGame(gameID)
		if err != nil {
			return store.Snapshot{}, err
		}
		branchName = game.ActiveBranch
	}
	snaps, err := m.Store.ListSnapshots(gameID, branchName)
	if err != nil {
		return store.Snapshot{}, err
	}
	if len(snaps) == 0 {
		return store.Snapshot{}, store.ErrNotFound
	}
	return snaps[0], nil
}

// ensureSavePathExists mirrors the JS pre-snapshot bootstrapping: if the
// save path is missing, create it — as a parent dir for file-like paths
// (has an extension), or as the directory itself.
func ensureSavePathExists(savePath string) error {
	if _, err := os.Stat(savePath); err == nil {
		return nil
	}
	if ext := filepath.Ext(savePath); len(ext) > 1 {
		return os.MkdirAll(filepath.Dir(savePath), 0o777)
	}
	return os.MkdirAll(savePath, 0o777)
}

// savePathHasContent reports whether there is anything at the save path
// worth safety-snapshotting: an existing file, or a non-empty directory.
func savePathHasContent(savePath string) bool {
	info, err := os.Stat(savePath)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return true
	}
	entries, err := os.ReadDir(savePath)
	return err == nil && len(entries) > 0
}

// clearSavePath removes a single save file, or empties a save directory
// while keeping the directory itself.
func clearSavePath(savePath string) error {
	info, err := os.Stat(savePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return os.Remove(savePath)
	}
	entries, err := os.ReadDir(savePath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(savePath, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
