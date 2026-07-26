package relay

import (
	"testing"

	"github.com/opensave/opensave/internal/p2p/syncengine"
)

// The relay's per-client budget and the sync engine's batch sizing are two
// halves of one number, edited in different packages. When they drifted apart
// the symptom was not a crash but a hang: a 25 MB file over the relay shed one
// response and then sat still, because the requester waits out its deadline
// for an answer that will never arrive.
//
// A peer pulling a file has ConcurrencyFor(true) requests outstanding, each
// answered by a batch of up to the target size, inflated by a third on the
// wire by base64. The relay has to be willing to hold that much for one
// client or it drops responses during a completely healthy transfer.
func TestRelayBudgetCoversConcurrentBlockFetches(t *testing.T) {
	concurrency := syncengine.ConcurrencyFor(true)

	// Largest batch the engine will ask for, across every block size it uses.
	var largestBatch int
	for _, blockSize := range []int{64 << 10, 512 << 10, 2 << 20} {
		indices := make([]int, 64)
		for i := range indices {
			indices[i] = i
		}
		batches := syncengine.BatchIndices(indices, blockSize, true)
		if n := len(batches[0]) * blockSize; n > largestBatch {
			largestBatch = n
		}
	}

	onWire := largestBatch * 4 / 3 // base64
	needed := concurrency * onWire

	if maxQueuedBytesPerClient < needed {
		t.Errorf("per-client budget is %d bytes but a healthy sync can deliver %d at once "+
			"(%d concurrent x %d-byte batch, +33%% base64) — the relay will shed responses "+
			"mid-transfer and each one costs the requester a full retry",
			maxQueuedBytesPerClient, needed, concurrency, largestBatch)
	}

	// One batch must also fit a single WebSocket frame on both ends.
	if onWire > 16<<20 {
		t.Errorf("a single batch is %d bytes on the wire, over the 16 MB frame limit", onWire)
	}

	// And the global budget has to allow more than one peer to sync at once.
	if maxQueuedBytesTotal < 2*maxQueuedBytesPerClient {
		t.Errorf("global budget %d leaves room for fewer than two syncing clients (per-client %d)",
			maxQueuedBytesTotal, maxQueuedBytesPerClient)
	}

	t.Logf("concurrency %d x %d KB batch = %d MB on the wire; per-client budget %d MB",
		concurrency, largestBatch>>10, needed>>20, maxQueuedBytesPerClient>>20)
}
