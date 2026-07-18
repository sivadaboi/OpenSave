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

	ts := m.now()
	snapshotID := "snap_" + strconv.FormatInt(ts.UnixMilli(), 10)
	zipPath := filepath.Join(backupDir, snapshotID+".zip")

	skipped, err := ZipPath(game.SavePath, zipPath)
	if err != nil {
		os.Remove(zipPath)
		return store.Snapshot{}, fmt.Errorf("zip save data: %w", err)
	}
	if len(skipped) > 0 && m.Log != nil {
		sample := skipped[0]
		m.Log("warn", fmt.Sprintf("snapshot of %q skipped %d unreadable file(s), e.g. %s", game.Name, len(skipped), sample))
	}
	info, err := os.Stat(zipPath)
	if err != nil {
		return store.Snapshot{}, err
	}

	if comment == "" {
		if isSystemAuto {
			comment = "Auto backup"
		} else {
			comment = "Manual snapshot"
		}
	}

	snap := store.Snapshot{
		ID:           snapshotID,
		GameID:       gameID,
		BranchName:   game.ActiveBranch,
		Timestamp:    ts.UTC().Format("2006-01-02T15:04:05.000Z"),
		Comment:      comment,
		IsSystemAuto: isSystemAuto,
		ZipPath:      zipPath,
		SizeBytes:    info.Size(),
	}
	if err := m.Store.CreateSnapshot(snap); err != nil {
		os.Remove(zipPath)
		return store.Snapshot{}, err
	}

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

// pruneRetention deletes the oldest snapshots (metadata + zip file,
// best-effort on the file) beyond the game's maxSnapshots limit. A limit
// of 0 or below disables pruning, matching the JS `maxSnapshots > 0` guard.
func (m *Manager) pruneRetention(game store.Game) {
	if game.MaxSnapshots <= 0 {
		return
	}
	beyond, err := m.Store.SnapshotsBeyondRetention(game.ID, game.ActiveBranch, game.MaxSnapshots)
	if err != nil {
		return
	}
	for _, snap := range beyond {
		if err := m.Store.DeleteSnapshot(snap.ID); err != nil {
			continue
		}
		os.Remove(snap.ZipPath) // best-effort, same as the JS app
	}
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
