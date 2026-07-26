package syncengine

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// instrumentedTransport wraps the fake peer to observe how the engine drives
// it: how many fetches overlap, and what is on disk part-way through.
type instrumentedTransport struct {
	*fakeTransport

	mu           sync.Mutex
	inFlight     int
	peakFlight   int
	calls        int
	onCall       func(call int)
	blocksServed int
	// failFrom makes every fetch from that call number onwards fail, which
	// is deterministic in a way that deleting the peer's file mid-transfer
	// is not — concurrent fetches may already have read it.
	failFrom int
}

func (t *instrumentedTransport) FetchBlocks(ctx context.Context, peer Peer, gameID, relPath string,
	blockIndices []int, blockSize int) ([]BlockData, error) {

	t.mu.Lock()
	t.calls++
	call := t.calls
	t.inFlight++
	if t.inFlight > t.peakFlight {
		t.peakFlight = t.inFlight
	}
	t.blocksServed += len(blockIndices)
	hook := t.onCall
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.inFlight--
		t.mu.Unlock()
	}()

	if hook != nil {
		hook(call)
	}
	if t.failFrom > 0 && call >= t.failFrom {
		return nil, errors.New("peer went away mid-transfer")
	}
	return t.fakeTransport.FetchBlocks(ctx, peer, gameID, relPath, blockIndices, blockSize)
}

// A large save must reach disk as it arrives. The engine used to collect
// every fetched block into one slice and only then call PatchFile, so pulling
// an N-byte file allocated N bytes of RAM first — a 1 GB save was a 1 GB
// allocation on a handheld with the game still resident.
func TestPullLargeFileStreamsToDiskWhileFetching(t *testing.T) {
	env := setupEngine(t)

	// 25 MB crosses delta's medium-file threshold, so this exercises the
	// 512 KB block path rather than the 64 KB one small saves use.
	payload := make([]byte, 25<<20)
	rng := rand.New(rand.NewSource(1))
	rng.Read(payload)

	remotePath := filepath.Join(env.remoteDir, "bigsave.bin")
	if err := os.WriteFile(remotePath, payload, 0o666); err != nil {
		t.Fatal(err)
	}

	inst := &instrumentedTransport{fakeTransport: env.transport}
	env.engine.Transport = inst

	localPath := filepath.Join(env.localDir, "bigsave.bin")
	tmpPath := localPath + delta.TmpSuffix

	// Sampled from inside a fetch, so it reflects the state mid-transfer.
	var sawPartialOnDisk bool
	var sawMu sync.Mutex
	inst.onCall = func(call int) {
		if call < 3 {
			return // let a couple of batches land first
		}
		if _, err := os.Stat(tmpPath); err == nil {
			sawMu.Lock()
			sawPartialOnDisk = true
			sawMu.Unlock()
		}
	}

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatalf("SyncWithPeer error = %v", err)
	}
	if res.Status != "updated" {
		t.Fatalf("result = %+v, want updated", res)
	}

	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("pulled file differs: got %d bytes, want %d", len(got), len(payload))
	}

	sawMu.Lock()
	partial := sawPartialOnDisk
	sawMu.Unlock()
	if !partial {
		t.Error("no partial file on disk during the transfer — blocks are still being buffered in memory")
	}

	// The temp file must not survive a successful patch.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file left behind at %s", tmpPath)
	}

	if inst.peakFlight < 2 {
		t.Errorf("peak concurrent block fetches = %d, want >1 — requests are not being pipelined", inst.peakFlight)
	}
	t.Logf("%d fetches, %d blocks, peak concurrency %d", inst.calls, inst.blocksServed, inst.peakFlight)
}

// A failure part-way through must leave the existing save untouched and clean
// up after itself, rather than committing a half-written file.
func TestPullLargeFileAbortsCleanlyOnFetchFailure(t *testing.T) {
	env := setupEngine(t)
	remotePath := filepath.Join(env.remoteDir, "bigsave.bin")
	localPath := filepath.Join(env.localDir, "bigsave.bin")

	// Sync once cleanly so the file is in both devices' lineage. Without
	// that, a differing local copy is a conflict and never reaches the
	// transfer path this test is about.
	original := bytes.Repeat([]byte("original-save-data!"), 200_000) // ~3.8 MB
	if err := os.WriteFile(remotePath, original, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	// Now the peer has a much larger version, and the transfer dies part-way.
	payload := make([]byte, 25<<20)
	rng := rand.New(rand.NewSource(2))
	rng.Read(payload)
	if err := os.WriteFile(remotePath, payload, 0o666); err != nil {
		t.Fatal(err)
	}

	inst := &instrumentedTransport{fakeTransport: env.transport, failFrom: 2}
	env.engine.Transport = inst

	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err == nil {
		t.Fatal("expected the sync to fail when the peer stops serving blocks mid-transfer")
	}

	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("the original save was destroyed by a failed pull: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("original save was modified by a failed pull: got %d bytes, want %d", len(got), len(original))
	}
	if _, err := os.Stat(localPath + delta.TmpSuffix); !os.IsNotExist(err) {
		t.Error("failed pull left its temp file behind")
	}
}
