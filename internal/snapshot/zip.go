package snapshot

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opensave/opensave/internal/store"
)

// ZipPath archives sourcePath into a ZIP at outPath. A directory is
// archived as its contents (entries relative to the directory root, not
// wrapped in a top-level folder — matching adm-zip's addLocalFolder); a
// single file is archived as one root-level entry (addLocalFile).
// Entries use the Store method (no compression), matching the JS app.
//
// Unreadable files (locked by a running game or AV, special/junction
// entries) are skipped and reported rather than failing the whole
// snapshot — but if nothing at all could be archived, that's an error.
func ZipPath(sourcePath, outPath string) (skipped []string, err error) {
	skipped, _, err = ZipPathCapturing(sourcePath, outPath)
	return skipped, err
}

// ZipPathCapturing is ZipPath, also reporting the hash of every file it
// archived so the caller can record what the snapshot holds. The hashes come
// free: the bytes pass through a hasher on their way into the archive.
func ZipPathCapturing(sourcePath, outPath string) (skipped []string, captured []store.CapturedFile, err error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("source path does not exist: %w", err)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return nil, nil, err
	}
	defer out.Close()

	w := zip.NewWriter(out)
	defer w.Close()

	if !info.IsDir() {
		name := filepath.Base(sourcePath)
		hash, addErr := addFileEntry(w, sourcePath, name)
		if addErr != nil {
			return nil, nil, addErr
		}
		return nil, []store.CapturedFile{{Path: name, Hash: hash}}, nil
	}

	archived := 0
	walkErr := filepath.Walk(sourcePath, func(path string, walkInfo os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// Unreadable subtree — record it and move on.
			if path != sourcePath {
				skipped = append(skipped, path)
				return nil
			}
			return walkErr
		}
		if path == sourcePath {
			return nil
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if walkInfo.IsDir() {
			// Explicit directory entries keep empty dirs restorable.
			if _, err := w.CreateHeader(&zip.FileHeader{Name: rel + "/", Method: zip.Store}); err != nil {
				return err
			}
			return nil
		}
		hash, addErr := addFileEntry(w, path, rel)
		if addErr != nil {
			skipped = append(skipped, path)
			return nil
		}
		captured = append(captured, store.CapturedFile{Path: rel, Hash: hash})
		archived++
		return nil
	})
	if walkErr != nil {
		return skipped, captured, walkErr
	}
	if archived == 0 && len(skipped) > 0 {
		return skipped, captured, fmt.Errorf("no files could be read (%d unreadable)", len(skipped))
	}
	return skipped, captured, nil
}

// The returned hash is the sha256 of the file's contents, hex encoded —
// deliberately the same value delta.FileEntry.Hash carries, so a recorded
// snapshot file can be compared against a manifest without either side
// re-reading anything.
//
// It costs nothing to produce here. The bytes are already streaming through
// this function on their way into the archive, so hashing them is a second
// writer on the same copy rather than a second pass over the disk.
func addFileEntry(w *zip.Writer, filePath, entryName string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	header := &zip.FileHeader{Name: entryName, Method: zip.Store}
	header.Modified = info.ModTime()

	entry, err := w.CreateHeader(header)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(entry, sum), f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// UnzipTo extracts a snapshot ZIP over targetPath. Single-file save mode
// (target is an existing file, or the archive holds exactly one root-level
// file and the target doesn't exist) extracts into the target's parent
// directory after removing the old file; directory mode clears the target
// directory and extracts into it — both matching unzipDirectory() in the
// JS app.
func UnzipTo(zipPath, targetPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer r.Close()

	isFile := false
	if info, statErr := os.Stat(targetPath); statErr == nil {
		isFile = !info.IsDir()
	} else if len(r.File) == 1 && !r.File[0].FileInfo().IsDir() {
		isFile = true
	}

	var destDir string
	if isFile {
		destDir = filepath.Dir(targetPath)
		if err := os.MkdirAll(destDir, 0o777); err != nil {
			return err
		}
		if _, statErr := os.Stat(targetPath); statErr == nil {
			_ = os.Chmod(targetPath, 0o666)
			if err := os.Remove(targetPath); err != nil {
				return fmt.Errorf("remove old save file: %w", err)
			}
		}
	} else {
		destDir = targetPath
		if err := os.MkdirAll(destDir, 0o777); err != nil {
			return err
		}
		if err := clearSavePath(destDir); err != nil {
			return fmt.Errorf("clear target dir: %w", err)
		}
	}

	for _, entry := range r.File {
		if err := extractEntry(entry, destDir); err != nil {
			return err
		}
	}
	return nil
}

func extractEntry(entry *zip.File, destDir string) error {
	return extractEntryAs(entry, destDir, entry.Name)
}

// extractEntryAs writes one entry under destDir at relName, which may differ
// from the entry's own name — a save location's files are stored inside the
// archive under a prefix and restored without it. The zip-slip check runs on
// the name actually being written, since that is the one that decides where
// bytes land.
func extractEntryAs(entry *zip.File, destDir, relName string) error {
	// Reject entries that would escape the destination (zip-slip).
	cleanName := filepath.Clean(filepath.FromSlash(relName))
	if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
		return fmt.Errorf("zip entry %q escapes destination", entry.Name)
	}
	destPath := filepath.Join(destDir, cleanName)

	if entry.FileInfo().IsDir() {
		return os.MkdirAll(destPath, 0o777)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o777); err != nil {
		return err
	}

	src, err := entry.Open()
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
