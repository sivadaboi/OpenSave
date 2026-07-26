package delta

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// PatchWriter reconstructs a file incrementally, so a sync never has to hold
// a whole file in memory. PatchFile's original shape took every fetched block
// as one []BlockSource, which meant pulling a 1 GB save allocated a 1 GB
// slice before a single byte reached disk — fine for a 200 KB save file,
// fatal on a Deck syncing a large one.
//
// Blocks land at their own offset via WriteAt, so they may arrive in any
// order and from any number of goroutines at once. That is what lets the
// fetcher pipeline requests instead of collecting them.
type PatchWriter struct {
	filePath  string
	tmpPath   string
	out       *os.File
	entry     FileEntry
	blockSize int64
	// size is the length the rebuilt file must end up at, summed from the
	// block list rather than taken from FileEntry.Size. The blocks are the
	// authority on content, callers legitimately build an entry without
	// setting Size, and a Size that disagreed with them would silently
	// truncate the result.
	size int64

	mu       sync.Mutex
	written  map[int]bool
	firstErr error
	closed   bool
}

// NewPatchWriter opens the temp file that filePath will be rebuilt into and
// sizes it up front, so out-of-order blocks can be written at their offsets.
func NewPatchWriter(filePath string, remoteEntry FileEntry) (*PatchWriter, error) {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o777); err != nil {
		return nil, fmt.Errorf("create parent dir: %w", err)
	}
	// Read-only save files must not fail the write, same as PatchFile.
	clearReadOnlyIfSet(filePath)

	blockSize := int64(remoteEntry.BlockSize)
	if blockSize <= 0 {
		blockSize = defaultBlockSize
	}

	var size int64
	for _, b := range remoteEntry.Blocks {
		size += int64(b.Length)
	}

	tmpPath := filePath + TmpSuffix
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o666)
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	if err := out.Truncate(size); err != nil {
		out.Close()
		removeWithRetry(tmpPath)
		return nil, fmt.Errorf("size temp file: %w", err)
	}

	return &PatchWriter{
		filePath:  filePath,
		tmpPath:   tmpPath,
		out:       out,
		entry:     remoteEntry,
		blockSize: blockSize,
		size:      size,
		written:   make(map[int]bool, len(remoteEntry.Blocks)),
	}, nil
}

// SeedUnchanged copies every block that isn't being fetched from the existing
// local file, which is what makes this a delta transfer rather than a full
// re-download. Blocks the peer is sending are skipped — WriteBlock fills them.
func (w *PatchWriter) SeedUnchanged(srcPath string, incoming map[int]bool) error {
	missing := make([]Block, 0, len(w.entry.Blocks))
	for _, block := range w.entry.Blocks {
		if !incoming[block.Index] {
			missing = append(missing, block)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	src, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%d block(s) were not sent by the peer and there is no local file to copy them from", len(missing))
		}
		return fmt.Errorf("open source file for unchanged blocks: %w", err)
	}
	defer src.Close()

	// One reusable buffer: these blocks are copied one at a time, so there is
	// no reason to allocate per block.
	buf := make([]byte, w.blockSize)
	for _, block := range missing {
		offset := int64(block.Index) * w.blockSize
		n := block.Length
		if int64(n) > w.blockSize {
			n = int(w.blockSize)
		}
		if _, err := src.ReadAt(buf[:n], offset); err != nil && err != io.EOF {
			return fmt.Errorf("read unchanged block %d from local file: %w", block.Index, err)
		}
		if err := w.WriteBlock(block.Index, buf[:n]); err != nil {
			return err
		}
	}
	return nil
}

// WriteBlock places one block's bytes at its offset. Safe to call
// concurrently: the writes target disjoint ranges of the same file.
func (w *PatchWriter) WriteBlock(index int, data []byte) error {
	offset := int64(index) * w.blockSize
	if end := offset + int64(len(data)); end > w.size {
		return w.fail(fmt.Errorf("block %d runs past the expected file size (%d > %d) — the peer's file changed mid-transfer",
			index, end, w.size))
	}
	if _, err := w.out.WriteAt(data, offset); err != nil {
		return w.fail(fmt.Errorf("write block %d: %w", index, err))
	}

	w.mu.Lock()
	w.written[index] = true
	w.mu.Unlock()
	return nil
}

func (w *PatchWriter) fail(err error) error {
	w.mu.Lock()
	if w.firstErr == nil {
		w.firstErr = err
	}
	w.mu.Unlock()
	return err
}

// Commit verifies the rebuilt file against the manifest's whole-file hash and
// only then atomically replaces the original. A failed check leaves the
// existing save untouched.
func (w *PatchWriter) Commit() error {
	w.mu.Lock()
	if w.firstErr != nil {
		err := w.firstErr
		w.mu.Unlock()
		w.Abort()
		return err
	}
	// Count against the manifest rather than comparing lengths: a peer that
	// sends an index the manifest doesn't list must not mask a real gap.
	missing := 0
	for _, b := range w.entry.Blocks {
		if !w.written[b.Index] {
			missing++
		}
	}
	w.mu.Unlock()

	if missing > 0 {
		w.Abort()
		return fmt.Errorf("patch incomplete for %s: %d of %d blocks never arrived",
			w.filePath, missing, len(w.entry.Blocks))
	}

	if err := w.out.Sync(); err != nil {
		w.Abort()
		return fmt.Errorf("flush patched file: %w", err)
	}
	if err := w.close(); err != nil {
		w.Abort()
		return fmt.Errorf("close patched file: %w", err)
	}

	gotHash, err := hashFileWhole(w.tmpPath)
	if err != nil {
		w.Abort()
		return fmt.Errorf("hash reconstructed file: %w", err)
	}
	if gotHash != w.entry.Hash {
		w.Abort()
		return fmt.Errorf("patch integrity check failed for %s: expected %s got %s",
			w.filePath, w.entry.Hash, gotHash)
	}

	clearReadOnlyIfSet(w.tmpPath)
	if err := replaceWithRetry(w.tmpPath, w.filePath); err != nil {
		removeWithRetry(w.tmpPath)
		return fmt.Errorf("finalize patched file: %w", err)
	}
	return nil
}

// Abort discards the partial reconstruction. Safe to call more than once, and
// safe to defer alongside Commit.
func (w *PatchWriter) Abort() {
	_ = w.close()
	removeWithRetry(w.tmpPath)
}

func (w *PatchWriter) close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	return w.out.Close()
}
