package api

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

// handleBackupExport bundles every snapshot ZIP (all games/branches) into
// one Brotli-compressed archive (.sscb) at the requested path.
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetPath string `json:"targetPath"`
	}
	if err := readJSON(r, &body); err != nil || body.TargetPath == "" {
		writeError(w, http.StatusBadRequest, "targetPath is required")
		return
	}

	outPath := body.TargetPath
	if !strings.HasSuffix(strings.ToLower(outPath), ".sscb") {
		outPath += ".sscb"
	}

	count, err := s.exportAllSnapshots(outPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Daemon.Log.Log("success", fmt.Sprintf("exported %d snapshot(s) to %s", count, outPath))
	writeJSON(w, http.StatusOK, map[string]any{"path": outPath, "snapshotCount": count})
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

// handleBackupRestore imports snapshots from an .sscb export: each inner
// entry is re-registered under its encoded game/branch (games must already
// be tracked; unknown games are skipped with a warning).
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourcePath string `json:"sourcePath"`
	}
	if err := readJSON(r, &body); err != nil || body.SourcePath == "" {
		writeError(w, http.StatusBadRequest, "sourcePath is required")
		return
	}

	imported, skipped, err := s.importBackup(body.SourcePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]int{"imported": imported, "skipped": skipped})
}

func (s *Server) importBackup(sourcePath string) (imported, skipped int, err error) {
	src, err := os.Open(sourcePath)
	if err != nil {
		return 0, 0, fmt.Errorf("open backup file: %w", err)
	}
	defer src.Close()

	// Decompress the Brotli stream to a temp file so zip.Reader can seek.
	tmp, err := os.CreateTemp("", "opensave-import-*.zip")
	if err != nil {
		return 0, 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, brotli.NewReader(src)); err != nil {
		tmp.Close()
		return 0, 0, fmt.Errorf("decompress backup: %w", err)
	}
	tmp.Close()

	zr, err := zip.OpenReader(tmpPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read backup archive (corrupt or not an .sscb file?): %w", err)
	}
	defer zr.Close()

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
