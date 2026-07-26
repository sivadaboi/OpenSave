package relay

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A client that stops draining its socket must not be able to make the relay
// buffer without limit on its behalf. The read limit allows 16 MB frames, so
// the old count-based queue (256 slots) bounded memory at gigabytes — enough
// for one stalled peer mid-transfer to take down a 512 MB instance and every
// other room with it.
func TestSlowClientCannotExceedQueueBudget(t *testing.T) {
	srv := New(Config{Port: 0})
	addr, err := srv.Start()
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	defer srv.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dial := func(device string) *websocket.Conn {
		c, _, err := websocket.Dial(ctx, "ws://"+addr+"/?room=budget-room&device="+device, nil)
		if err != nil {
			t.Fatalf("dial %s: %v", device, err)
		}
		c.SetReadLimit(16 << 20)
		return c
	}

	sender := dial("sender")
	defer sender.Close(websocket.StatusNormalClosure, "")
	// The receiver connects and then never reads, which is what a peer looks
	// like when its process is paused, its link stalls, or it's mid-GC.
	stalled := dial("stalled")
	defer stalled.Close(websocket.StatusNormalClosure, "")

	payload := bytes.Repeat([]byte("x"), 2<<20) // 2 MiB, about one real block batch
	const messages = 40                         // 80 MiB if nothing bounded it

	for i := 0; i < messages; i++ {
		writeCtx, cancelWrite := context.WithTimeout(ctx, 10*time.Second)
		err := sender.Write(writeCtx, websocket.MessageText, payload)
		cancelWrite()
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if queued := srv.totalQueued.Load(); queued > maxQueuedBytesTotal {
			t.Fatalf("global queue reached %d bytes, over the %d budget", queued, maxQueuedBytesTotal)
		}
	}

	// Let the relay finish processing everything the sender pushed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		seen := srv.totalMessages
		srv.mu.Unlock()
		if seen >= messages {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	srv.mu.Lock()
	dropped := srv.droppedMessages
	srv.mu.Unlock()

	// The stalled client's own queue is what must stay bounded.
	srv.mu.Lock()
	var peak int64
	for _, room := range srv.rooms {
		for c := range room {
			if q := c.queued.Load(); q > peak {
				peak = q
			}
		}
	}
	srv.mu.Unlock()

	if peak > maxQueuedBytesPerClient {
		t.Errorf("a single client queued %d bytes, over the %d per-client budget", peak, maxQueuedBytesPerClient)
	}
	if dropped == 0 {
		t.Errorf("expected the relay to shed messages for a client that never reads, but none were dropped")
	}
	t.Logf("dropped %d/%d messages, peak per-client queue %d bytes", dropped, messages, peak)
}

// A disconnecting client's queued bytes have to go back to the global budget,
// or ordinary connection churn slowly starves the relay until it can't
// forward anything.
func TestDisconnectReleasesQueueBudget(t *testing.T) {
	srv := New(Config{Port: 0})
	addr, err := srv.Start()
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	defer srv.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	payload := bytes.Repeat([]byte("y"), 2<<20)

	for round := 0; round < 3; round++ {
		sender, _, err := websocket.Dial(ctx, "ws://"+addr+"/?room=churn-room&device=sender", nil)
		if err != nil {
			t.Fatalf("dial sender: %v", err)
		}
		stalled, _, err := websocket.Dial(ctx, "ws://"+addr+"/?room=churn-room&device=stalled", nil)
		if err != nil {
			t.Fatalf("dial stalled: %v", err)
		}

		for i := 0; i < 8; i++ {
			writeCtx, cancelWrite := context.WithTimeout(ctx, 10*time.Second)
			_ = sender.Write(writeCtx, websocket.MessageText, payload)
			cancelWrite()
		}

		sender.Close(websocket.StatusNormalClosure, "")
		stalled.Close(websocket.StatusNormalClosure, "")
	}

	// Once every client is gone, nothing may still be charged to the budget.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		empty := len(srv.rooms) == 0
		srv.mu.Unlock()
		if empty && srv.totalQueued.Load() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	srv.mu.Lock()
	rooms := len(srv.rooms)
	srv.mu.Unlock()
	t.Fatalf("after all clients disconnected: %d bytes still charged to the global budget (%d rooms left)",
		srv.totalQueued.Load(), rooms)
}
