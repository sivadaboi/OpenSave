package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// BlockSource supplies the bytes for one block index, either from the
// existing local file (unchanged blocks) or from freshly-fetched remote
// data (changed blocks). Callers pass a map of remote-fetched blocks; any
// index not present is read from the existing local file.
type BlockSource struct {
	Index int
	Data  []byte // non-nil only for blocks fetched from the remote peer
}

// PatchFile reconstructs filePath from a mix of its own existing content
// (for blocks that didn't change) and freshly supplied remote block data,
// verifies the whole-file hash against remoteEntry.Hash, and atomically
// replaces the original file only once the reconstruction is verified.
//
// Unlike the original JS implementation (unlink then rename, leaving a
// brief window with no file present if the process dies mid-patch), this
// relies on os.Rename's platform behavior: on Windows it calls
// MoveFileEx with MOVEFILE_REPLACE_EXISTING, and on POSIX rename(2) is
// already atomic — so the destination is replaced in one step with no gap.
func PatchFile(filePath string, remoteEntry FileEntry, remoteBlocks []BlockSource) (err error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	// Read-only save files must not fail the write; clear the attribute
	// first the same way the JS code chmod 0o666's before write/unlink.
	clearReadOnlyIfSet(filePath)

	tmpPath := filePath + TmpSuffix
	if err := writeReconstructedFile(tmpPath, filePath, remoteEntry, remoteBlocks); err != nil {
		removeWithRetry(tmpPath)
		return err
	}

	gotHash, err := hashFileWhole(tmpPath)
	if err != nil {
		removeWithRetry(tmpPath)
		return fmt.Errorf("hash reconstructed file: %w", err)
	}
	if gotHash != remoteEntry.Hash {
		removeWithRetry(tmpPath)
		return fmt.Errorf("patch integrity check failed for %s: expected %s got %s", filePath, remoteEntry.Hash, gotHash)
	}

	clearReadOnlyIfSet(tmpPath)

	if err := replaceWithRetry(tmpPath, filePath); err != nil {
		removeWithRetry(tmpPath)
		return fmt.Errorf("finalize patched file: %w", err)
	}
	return nil
}

// renameFile is swappable so tests can simulate a transiently locked
// destination without real AV timing.
var renameFile = os.Rename

// replaceWithRetry renames tmpPath over filePath, retrying with backoff
// (~6s total). On Windows, antivirus — Defender above all — briefly locks
// freshly written files (especially .exe payloads) for scanning, which
// makes the finalizing rename fail with a sharing violation even though
// nothing is actually wrong. One failed rename here used to strand the
// .opensave.tmp file on disk, which then leaked into manifests and synced
// to peers as a real file.
func replaceWithRetry(tmpPath, filePath string) error {
	var err error
	delay := 100 * time.Millisecond
	for attempt := 0; attempt < 7; attempt++ {
		if attempt > 0 {
			time.Sleep(delay)
			delay *= 2
		}
		clearReadOnlyIfSet(filePath)
		if err = renameFile(tmpPath, filePath); err == nil {
			return nil
		}
	}
	return err
}

// removeWithRetry best-effort deletes a temp file, retrying briefly since
// the same AV scan that fails a rename also fails the cleanup delete. A
// survivor is harmless — manifests exclude TmpSuffix files and the walk
// garbage-collects stale ones — but tidy is better.
func removeWithRetry(path string) {
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			return
		}
	}
}

// writeReconstructedFile writes blockCount blocks (each remoteEntry.BlockSize
// bytes, except possibly the last) to tmpPath: incoming blocks from
// remoteBlocks where supplied, otherwise the corresponding byte range read
// from the existing srcPath.
func writeReconstructedFile(tmpPath, srcPath string, remoteEntry FileEntry, remoteBlocks []BlockSource) error {
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o666)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer out.Close()

	supplied := make(map[int][]byte, len(remoteBlocks))
	for _, b := range remoteBlocks {
		supplied[b.Index] = b.Data
	}

	var src *os.File
	if existing, statErr := os.Stat(srcPath); statErr == nil && !existing.IsDir() {
		src, err = os.Open(srcPath)
		if err != nil {
			return fmt.Errorf("open source file for unchanged blocks: %w", err)
		}
		defer src.Close()
	}

	for _, block := range remoteEntry.Blocks {
		if data, ok := supplied[block.Index]; ok {
			if _, err := out.Write(data); err != nil {
				return fmt.Errorf("write block %d: %w", block.Index, err)
			}
			continue
		}
		if src == nil {
			return fmt.Errorf("block %d not supplied and no local source file to copy it from", block.Index)
		}
		offset := int64(block.Index) * int64(remoteEntry.BlockSize)
		buf := make([]byte, block.Length)
		if _, err := src.ReadAt(buf, offset); err != nil && err != io.EOF {
			return fmt.Errorf("read unchanged block %d from local file: %w", block.Index, err)
		}
		if _, err := out.Write(buf); err != nil {
			return fmt.Errorf("write copied block %d: %w", block.Index, err)
		}
	}
	return nil
}

func hashFileWhole(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadBlocks returns the requested block indices from a file, chunked at
// blockSize. Serves the P2P /blocks route: peers request only the indices
// that differ.
func ReadBlocks(filePath string, blockIndices []int, blockSize int) ([]BlockSource, error) {
	if blockSize <= 0 {
		blockSize = defaultBlockSize
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	var out []BlockSource
	for _, idx := range blockIndices {
		offset := int64(idx) * int64(blockSize)
		if offset >= info.Size() {
			continue
		}
		length := int64(blockSize)
		if offset+length > info.Size() {
			length = info.Size() - offset
		}
		buf := make([]byte, length)
		if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
			return nil, fmt.Errorf("read block %d: %w", idx, err)
		}
		out = append(out, BlockSource{Index: idx, Data: buf})
	}
	return out, nil
}

// clearReadOnlyIfSet mirrors fs.chmodSync(path, 0o666): on Windows this
// clears FILE_ATTRIBUTE_READONLY (the only bit os.Chmod affects there), on
// POSIX it grants owner/group/other read+write. Errors are intentionally
// swallowed — the file may not exist yet (first write) which is fine.
func clearReadOnlyIfSet(path string) {
	_ = os.Chmod(path, 0o666)
}
