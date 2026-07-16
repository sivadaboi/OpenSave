package snapshot

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("source path does not exist: %w", err)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	w := zip.NewWriter(out)
	defer w.Close()

	if !info.IsDir() {
		return nil, addFileEntry(w, sourcePath, filepath.Base(sourcePath))
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
		if err := addFileEntry(w, path, rel); err != nil {
			skipped = append(skipped, path)
			return nil
		}
		archived++
		return nil
	})
	if walkErr != nil {
		return skipped, walkErr
	}
	if archived == 0 && len(skipped) > 0 {
		return skipped, fmt.Errorf("no files could be read (%d unreadable)", len(skipped))
	}
	return skipped, nil
}

func addFileEntry(w *zip.Writer, filePath, entryName string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	header := &zip.FileHeader{Name: entryName, Method: zip.Store}
	header.Modified = info.ModTime()

	entry, err := w.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, f)
	return err
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
	// Reject entries that would escape the destination (zip-slip).
	cleanName := filepath.Clean(filepath.FromSlash(entry.Name))
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
