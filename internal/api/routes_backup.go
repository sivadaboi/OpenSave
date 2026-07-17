package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/snapshot"
)

// handleSnapshotFiles lists the entries inside a snapshot ZIP (for the
// granular-restore browser in the UI).
func (s *Server) handleSnapshotFiles(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	snapshotID := chi.URLParam(r, "snapshotId")

	snap, err := s.Daemon.Store.GetSnapshot(snapshotID)
	if err != nil || snap.GameID != gameID {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}

	zr, err := zip.OpenReader(snap.ZipPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("open snapshot zip: %v", err))
		return
	}
	defer zr.Close()

	type fileEntry struct {
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		IsDir bool   `json:"isDir"`
	}
	files := []fileEntry{}
	for _, f := range zr.File {
		files = append(files, fileEntry{
			Path:  f.Name,
			Size:  int64(f.UncompressedSize64),
			IsDir: f.FileInfo().IsDir(),
		})
	}
	writeJSON(w, http.StatusOK, files)
}

// handleRestoreFile restores a single file out of a snapshot into the live
// save location, taking a safety snapshot first.
func (s *Server) handleRestoreFile(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	snapshotID := chi.URLParam(r, "snapshotId")

	var body struct {
		RelPath string `json:"relPath"`
	}
	if err := readJSON(r, &body); err != nil || body.RelPath == "" {
		writeError(w, http.StatusBadRequest, "relPath is required")
		return
	}

	game, err := s.Daemon.Store.GetGame(gameID)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}
	snap, err := s.Daemon.Store.GetSnapshot(snapshotID)
	if err != nil || snap.GameID != gameID {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if !delta.IsSafePath(game.SavePath, body.RelPath) {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	safetyComment := fmt.Sprintf("Safety snapshot before restoring file %q from %s", body.RelPath, snapshotID)
	if _, err := s.Daemon.Snapshots.Create(gameID, safetyComment, true); err != nil {
		s.Daemon.Log.Log("warn", "safety snapshot before file restore failed: "+err.Error())
	}

	if err := extractSingleFile(snap.ZipPath, body.RelPath, game.SavePath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// extractSingleFile pulls one entry from a snapshot ZIP into the save
// location (savePath may be a directory root or, for single-file saves,
// the file itself).
func extractSingleFile(zipPath, relPath, savePath string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open snapshot zip: %w", err)
	}
	defer zr.Close()

	want := strings.ReplaceAll(relPath, "\\", "/")
	for _, f := range zr.File {
		if strings.ReplaceAll(f.Name, "\\", "/") != want {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		defer src.Close()

		destPath := filepath.Join(savePath, filepath.FromSlash(want))
		if info, statErr := os.Stat(savePath); statErr == nil && !info.IsDir() {
			destPath = savePath // single-file save mode
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o777); err != nil {
			return err
		}
		_ = os.Chmod(destPath, 0o666)
		dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
		if err != nil {
			return err
		}
		defer dst.Close()
		_, err = io.Copy(dst, src)
		return err
	}
	return fmt.Errorf("file %q not found in snapshot", relPath)
}

// ── .sscb format v2 ──────────────────────────────────────────────────
//
// A v2 export is still a Brotli-compressed ZIP, but instead of the whole
// snapshot library it carries the LIVE save of each selected game
// (captured at export time) plus a manifest that records where each save
// belongs — both the absolute path and a machine-portable tokenized form
// (%USERPROFILE%/…, $HOME/…) so a different user account or PC can
// restore to the right place. v1 archives (bare gameId__branch__snapId
// entries, no manifest) still import through the legacy path below.

const backupManifestName = "manifest.json"

type backupManifest struct {
	Version    int                  `json:"version"`
	ExportedAt string               `json:"exportedAt"`
	OS         string               `json:"os"`
	Games      []backupManifestGame `json:"games"`
}

type backupManifestGame struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AppID        string `json:"appId,omitempty"`
	Tracked      bool   `json:"tracked"`
	SavePath     string `json:"savePath"`
	PortablePath string `json:"portablePath"`
}

// skippedGame reports one game an export/import couldn't include and why.
type skippedGame struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

// handleBackupExport writes an .sscb at the requested path. With a games
// selection it exports those games' live saves (v2); without one it
// falls back to the legacy whole-snapshot-library export.
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetPath string `json:"targetPath"`
		Games      []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			AppID    string `json:"appId"`
			SavePath string `json:"savePath"`
		} `json:"games"`
	}
	if err := readJSON(r, &body); err != nil || body.TargetPath == "" {
		writeError(w, http.StatusBadRequest, "targetPath is required")
		return
	}

	outPath := body.TargetPath
	if !strings.HasSuffix(strings.ToLower(outPath), ".sscb") {
		outPath += ".sscb"
	}

	if len(body.Games) == 0 {
		count, err := s.exportAllSnapshots(outPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.Daemon.Log.Log("success", fmt.Sprintf("exported %d snapshot(s) to %s", count, outPath))
		writeJSON(w, http.StatusOK, map[string]any{"path": outPath, "snapshotCount": count})
		return
	}

	// Resolve the selection: tracked games come from the store (its data
	// is authoritative); untracked ones (auto-scan discoveries) use the
	// caller-provided name/path after validation.
	var games []backupManifestGame
	var skipped []skippedGame
	seenID := map[string]bool{}
	seenPath := map[string]bool{}
	for _, req := range body.Games {
		if req.ID == "" || seenID[req.ID] {
			continue
		}
		seenID[req.ID] = true

		entry := backupManifestGame{ID: req.ID, Name: req.Name, AppID: req.AppID}
		if g, err := s.Daemon.Store.GetGame(req.ID); err == nil {
			entry.Name, entry.AppID, entry.SavePath, entry.Tracked = g.Name, g.AppID, g.SavePath, true
		} else {
			abs, err := s.Daemon.CheckRestoreTarget(req.SavePath)
			if err != nil {
				skipped = append(skipped, skippedGame{req.ID, req.Name, err.Error()})
				continue
			}
			if _, err := os.Stat(abs); err != nil {
				skipped = append(skipped, skippedGame{req.ID, req.Name, "save path does not exist: " + abs})
				continue
			}
			if entry.Name == "" {
				entry.Name = req.ID
			}
			entry.SavePath = abs
		}
		norm := strings.ToLower(filepath.Clean(entry.SavePath))
		if seenPath[norm] {
			continue
		}
		seenPath[norm] = true
		entry.PortablePath = tokenizeSavePath(entry.SavePath)
		games = append(games, entry)
	}

	exported, exportSkipped, err := s.exportSelectedSaves(outPath, games)
	skipped = append(skipped, exportSkipped...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Daemon.Log.Log("success", fmt.Sprintf("exported the saves of %d game(s) to %s (%d skipped)", len(exported), outPath, len(skipped)))
	writeJSON(w, http.StatusOK, map[string]any{
		"path": outPath, "exported": len(exported), "skipped": skipped,
	})
}

// exportSelectedSaves zips each game's live save and writes the v2
// archive: saves/<gameID>.zip entries plus the manifest.
func (s *Server) exportSelectedSaves(outPath string, games []backupManifestGame) (exported []backupManifestGame, skipped []skippedGame, err error) {
	out, err := os.Create(outPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create export file: %w", err)
	}
	defer out.Close()

	bw := brotli.NewWriterLevel(out, 9)
	defer bw.Close()
	zw := zip.NewWriter(bw)
	defer zw.Close()

	for _, g := range games {
		tmp, tmpErr := os.CreateTemp("", "opensave-export-*.zip")
		if tmpErr != nil {
			return exported, skipped, tmpErr
		}
		tmpPath := tmp.Name()
		tmp.Close()

		skippedFiles, zipErr := snapshot.ZipPath(g.SavePath, tmpPath)
		if zipErr != nil {
			os.Remove(tmpPath)
			skipped = append(skipped, skippedGame{g.ID, g.Name, zipErr.Error()})
			continue
		}
		if len(skippedFiles) > 0 {
			s.Daemon.Log.Log("warn", fmt.Sprintf("export of %q skipped %d unreadable file(s)", g.Name, len(skippedFiles)))
		}

		entry, entryErr := zw.CreateHeader(&zip.FileHeader{Name: "saves/" + g.ID + ".zip", Method: zip.Store})
		if entryErr == nil {
			var src *os.File
			if src, entryErr = os.Open(tmpPath); entryErr == nil {
				_, entryErr = io.Copy(entry, src)
				src.Close()
			}
		}
		os.Remove(tmpPath)
		if entryErr != nil {
			return exported, skipped, entryErr
		}
		exported = append(exported, g)
	}

	manifest := backupManifest{
		Version:    2,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		OS:         runtime.GOOS,
		Games:      exported,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return exported, skipped, err
	}
	mEntry, err := zw.CreateHeader(&zip.FileHeader{Name: backupManifestName, Method: zip.Deflate})
	if err != nil {
		return exported, skipped, err
	}
	if _, err := mEntry.Write(raw); err != nil {
		return exported, skipped, err
	}
	return exported, skipped, nil
}

// exportAllSnapshots writes an inner ZIP (one entry per snapshot zip,
// named gameId__branch__snapId.zip, mirroring the cloud naming scheme)
// compressed with Brotli.
func (s *Server) exportAllSnapshots(outPath string) (int, error) {
	out, err := os.Create(outPath)
	if err != nil {
		return 0, fmt.Errorf("create export file: %w", err)
	}
	defer out.Close()

	bw := brotli.NewWriterLevel(out, 9)
	defer bw.Close()
	zw := zip.NewWriter(bw)
	defer zw.Close()

	games, err := s.Daemon.Store.ListGames()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, game := range games {
		branches, err := s.Daemon.Store.ListBranches(game.ID)
		if err != nil {
			return count, err
		}
		for _, branch := range branches {
			snaps, err := s.Daemon.Store.ListSnapshots(game.ID, branch)
			if err != nil {
				return count, err
			}
			for _, snap := range snaps {
				src, err := os.Open(snap.ZipPath)
				if err != nil {
					s.Daemon.Log.Log("warn", fmt.Sprintf("skipping missing snapshot zip %s", snap.ZipPath))
					continue
				}
				entryName := fmt.Sprintf("%s__%s__%s.zip", game.ID, branch, snap.ID)
				entry, err := zw.CreateHeader(&zip.FileHeader{Name: entryName, Method: zip.Store})
				if err != nil {
					src.Close()
					return count, err
				}
				if _, err := io.Copy(entry, src); err != nil {
					src.Close()
					return count, err
				}
				src.Close()
				count++
			}
		}
	}
	return count, nil
}

// handleBackupRestore imports an .sscb export. v2 archives (with a
// manifest) support two modes:
//
//   - "snapshots" (default): each game's exported save is added to its
//     snapshot history — live save files are never touched. Games not
//     tracked on this machine are reported and skipped.
//   - "overwrite": every game's exported save is restored onto disk.
//     Tracked games restore to their locally tracked path with a safety
//     snapshot first; untracked games restore to the manifest's path
//     (adapted to this machine) with any existing content zipped to a
//     safety backup first. Nothing is ever overwritten without a copy.
//
// Legacy v1 archives keep their original snapshot-library import.
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourcePath string `json:"sourcePath"`
		Mode       string `json:"mode"`
	}
	if err := readJSON(r, &body); err != nil || body.SourcePath == "" {
		writeError(w, http.StatusBadRequest, "sourcePath is required")
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = "snapshots"
	}
	if mode != "snapshots" && mode != "overwrite" {
		writeError(w, http.StatusBadRequest, `mode must be "snapshots" or "overwrite"`)
		return
	}

	tmpZip, cleanup, err := decompressBackup(body.SourcePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cleanup()

	zr, err := zip.OpenReader(tmpZip)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read backup archive (corrupt or not an .sscb file?): %v", err))
		return
	}
	defer zr.Close()

	manifest := readBackupManifest(&zr.Reader)
	if manifest == nil {
		// v1 archive: snapshot-library import, mode does not apply.
		imported, skipped, err := s.importLegacyBackup(&zr.Reader)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.BroadcastGamesUpdate()
		writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped, "legacy": true})
		return
	}

	results := s.importBackupV2(&zr.Reader, manifest, mode)
	restored, snapshotted, skipped := 0, 0, 0
	for _, res := range results {
		switch res.Action {
		case "restored":
			restored++
		case "snapshot":
			snapshotted++
		default:
			skipped++
		}
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": mode, "restored": restored, "snapshots": snapshotted,
		"skipped": skipped, "results": results,
	})
}

// decompressBackup expands the Brotli stream to a temp file (zip.Reader
// needs to seek). The returned cleanup removes the temp file.
func decompressBackup(sourcePath string) (string, func(), error) {
	src, err := os.Open(sourcePath)
	if err != nil {
		return "", nil, fmt.Errorf("open backup file: %w", err)
	}
	defer src.Close()

	tmp, err := os.CreateTemp("", "opensave-import-*.zip")
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, brotli.NewReader(src)); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", nil, fmt.Errorf("decompress backup: %w", err)
	}
	tmp.Close()
	return tmpPath, func() { os.Remove(tmpPath) }, nil
}

// readBackupManifest returns the parsed v2 manifest, or nil for v1 files.
func readBackupManifest(zr *zip.Reader) *backupManifest {
	for _, f := range zr.File {
		if f.Name != backupManifestName {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return nil
		}
		defer src.Close()
		var m backupManifest
		if err := json.NewDecoder(src).Decode(&m); err != nil || m.Version < 2 {
			return nil
		}
		return &m
	}
	return nil
}

// importResult is one game's outcome, mirrored into the activity log.
type importResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Tracked bool   `json:"tracked"`
	Action  string `json:"action"` // restored | snapshot | skipped
	Path    string `json:"path,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) importBackupV2(zr *zip.Reader, manifest *backupManifest, mode string) []importResult {
	saves := map[string]*zip.File{}
	for _, f := range zr.File {
		if id, ok := strings.CutPrefix(f.Name, "saves/"); ok && strings.HasSuffix(id, ".zip") {
			saves[strings.TrimSuffix(id, ".zip")] = f
		}
	}

	settings, err := s.Daemon.Store.GetSettings()
	if err != nil {
		return []importResult{{Action: "skipped", Error: err.Error()}}
	}

	var results []importResult
	baseMs := time.Now().UnixMilli()
	for i, g := range manifest.Games {
		res := importResult{ID: g.ID, Name: g.Name}
		entry, ok := saves[g.ID]
		if !ok {
			res.Action, res.Error = "skipped", "save data missing from backup file"
			results = append(results, s.logImportResult(res))
			continue
		}

		// Stage the inner save zip on disk.
		tmp, err := os.CreateTemp("", "opensave-import-save-*.zip")
		if err != nil {
			res.Action, res.Error = "skipped", err.Error()
			results = append(results, s.logImportResult(res))
			continue
		}
		tmpPath := tmp.Name()
		tmp.Close()
		if err := extractZipEntryToFile(entry, tmpPath); err != nil {
			os.Remove(tmpPath)
			res.Action, res.Error = "skipped", err.Error()
			results = append(results, s.logImportResult(res))
			continue
		}

		local, err := s.Daemon.Store.GetGame(g.ID)
		res.Tracked = err == nil

		if res.Tracked {
			// The imported state always lands in snapshot history first;
			// overwrite mode then restores that snapshot through the
			// standard path (which takes its own pre-restore safety
			// snapshot of the current save).
			branch := local.ActiveBranch
			if branch == "" {
				branch = "main"
			}
			snapID := fmt.Sprintf("snap_%d", baseMs+int64(i))
			destDir := filepath.Join(settings.BackupsDir, g.ID, branch)
			destPath := filepath.Join(destDir, snapID+".zip")
			err := os.MkdirAll(destDir, 0o777)
			if err == nil {
				err = copyFile(tmpPath, destPath)
			}
			var size int64
			if err == nil {
				if info, statErr := os.Stat(destPath); statErr == nil {
					size = info.Size()
				}
				err = s.Daemon.EnsureImportedSnapshot(g.ID, branch, snapID, destPath, size)
			}
			if err == nil && mode == "overwrite" {
				_, err = s.Daemon.Snapshots.Restore(g.ID, snapID)
				res.Path = local.SavePath
				res.Action = "restored"
			} else if err == nil {
				res.Path = local.SavePath
				res.Action = "snapshot"
			}
			if err != nil {
				res.Action, res.Error = "skipped", err.Error()
			}
			os.Remove(tmpPath)
			results = append(results, s.logImportResult(res))
			continue
		}

		// Not tracked here.
		if mode == "snapshots" {
			os.Remove(tmpPath)
			res.Action = "skipped"
			res.Error = "not tracked on this machine — track it and re-import, or use overwrite mode"
			results = append(results, s.logImportResult(res))
			continue
		}
		if manifest.OS != runtime.GOOS {
			os.Remove(tmpPath)
			res.Action = "skipped"
			res.Error = fmt.Sprintf("backup was exported on %s — its save paths don't apply on %s", manifest.OS, runtime.GOOS)
			results = append(results, s.logImportResult(res))
			continue
		}

		target := resolvePortablePath(g.PortablePath)
		if target == "" {
			target = g.SavePath
		}
		target, err = s.Daemon.CheckRestoreTarget(target)
		if err != nil {
			os.Remove(tmpPath)
			res.Action, res.Error = "skipped", err.Error()
			results = append(results, s.logImportResult(res))
			continue
		}
		res.Path = target

		// Never overwrite untracked content without a copy: zip whatever
		// is there now into the safety folder first, and skip the restore
		// entirely if that safety copy cannot be written.
		if pathHasContent(target) {
			safetyDir := filepath.Join(settings.BackupsDir, "_import-safety")
			safetyPath := filepath.Join(safetyDir, fmt.Sprintf("%s-%d.zip", sanitizeFilename(g.ID), baseMs))
			if err := os.MkdirAll(safetyDir, 0o777); err == nil {
				_, err = snapshot.ZipPath(target, safetyPath)
			}
			if err != nil {
				os.Remove(tmpPath)
				res.Action = "skipped"
				res.Error = "couldn't take a safety copy of the existing files, refusing to overwrite: " + err.Error()
				results = append(results, s.logImportResult(res))
				continue
			}
			s.Daemon.Log.Log("info", fmt.Sprintf("existing files at %s backed up to %s", target, safetyPath))
		}

		err = snapshot.UnzipTo(tmpPath, target)
		os.Remove(tmpPath)
		if err != nil {
			res.Action, res.Error = "skipped", err.Error()
		} else {
			res.Action = "restored"
		}
		results = append(results, s.logImportResult(res))
	}
	return results
}

// logImportResult mirrors one import outcome into the activity feed —
// which game, where its files went (full path), and its tracked status.
func (s *Server) logImportResult(res importResult) importResult {
	tracked := "not tracked"
	if res.Tracked {
		tracked = "tracked"
	}
	switch res.Action {
	case "restored":
		s.Daemon.Log.Log("success", fmt.Sprintf("backup import: restored %q → %s (%s)", res.Name, res.Path, tracked))
	case "snapshot":
		s.Daemon.Log.Log("success", fmt.Sprintf("backup import: added %q to snapshots (%s)", res.Name, tracked))
	default:
		s.Daemon.Log.Log("warn", fmt.Sprintf("backup import: skipped %q (%s) — %s", res.Name, tracked, res.Error))
	}
	return res
}

// pathHasContent reports whether target holds anything worth a safety
// copy: an existing file, or a directory with at least one entry.
func pathHasContent(target string) bool {
	info, err := os.Stat(target)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return true
	}
	entries, err := os.ReadDir(target)
	return err == nil && len(entries) > 0
}

// copyFile copies src to dst (creating/truncating dst).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// sanitizeFilename strips path separators and oddities from an ID used
// in a filename.
func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, name)
}

// importLegacyBackup handles v1 archives: each entry is a snapshot zip
// named gameId__branch__snapId.zip, re-registered into the local library
// (games must already be tracked; unknown games are skipped).
func (s *Server) importLegacyBackup(zr *zip.Reader) (imported, skipped int, err error) {
	settings, err := s.Daemon.Store.GetSettings()
	if err != nil {
		return 0, 0, err
	}

	for _, f := range zr.File {
		gameID, branch, snapID, ok := snapshot.ParseExportEntryName(f.Name)
		if !ok {
			skipped++
			continue
		}
		if _, err := s.Daemon.Store.GetGame(gameID); err != nil {
			s.Daemon.Log.Log("warn", fmt.Sprintf("backup entry %s: game not tracked, skipping", f.Name))
			skipped++
			continue
		}

		destDir := filepath.Join(settings.BackupsDir, gameID, branch)
		if err := os.MkdirAll(destDir, 0o777); err != nil {
			return imported, skipped, err
		}
		destPath := filepath.Join(destDir, snapID+".zip")

		if err := extractZipEntryToFile(f, destPath); err != nil {
			return imported, skipped, err
		}
		info, err := os.Stat(destPath)
		if err != nil {
			return imported, skipped, err
		}

		if err := s.Daemon.EnsureImportedSnapshot(gameID, branch, snapID, destPath, info.Size()); err != nil {
			s.Daemon.Log.Log("warn", fmt.Sprintf("backup entry %s: %v", f.Name, err))
			skipped++
			continue
		}
		imported++
	}
	return imported, skipped, nil
}

func extractZipEntryToFile(f *zip.File, destPath string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}
