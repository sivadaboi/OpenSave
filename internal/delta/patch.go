package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
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
// It is the all-at-once form of PatchWriter, kept for callers that already
// hold every block (single small files, tests, backup restore). The sync
// engine streams through PatchWriter directly instead, so a large file never
// has to fit in memory.
func PatchFile(filePath string, remoteEntry FileEntry, remoteBlocks []BlockSource) error {
	w, err := NewPatchWriter(filePath, remoteEntry)
	if err != nil {
		return err
	}

	incoming := make(map[int]bool, len(remoteBlocks))
	for _, b := range remoteBlocks {
		incoming[b.Index] = true
	}
	for _, b := range remoteBlocks {
		if err := w.WriteBlock(b.Index, b.Data); err != nil {
			w.Abort()
			return err
		}
	}
	if err := w.SeedUnchanged(filePath, incoming); err != nil {
		w.Abort()
		return err
	}
	return w.Commit()
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
