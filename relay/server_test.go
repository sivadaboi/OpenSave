package relay

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestRelayForwardsLargeMessages guards the read-limit bug that broke
// every real-world sync: coder/websocket defaults to a 32 KB read limit,
// so without an explicit SetReadLimit the relay killed a peer's
// connection as soon as it sent a block payload (~1.5 MB). Manifests
// passed, transfers died, syncs looped forever on reconnect-retry.
func TestRelayForwardsLargeMessages(t *testing.T) {
	srv := New(Config{Port: 0})
	addr, err := srv.Start()
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	defer srv.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dial := func(device string) *websocket.Conn {
		c, _, err := websocket.Dial(ctx, "ws://"+addr+"/?room=big-msg-room&device="+device, nil)
		if err != nil {
			t.Fatalf("dial %s: %v", device, err)
		}
		c.SetReadLimit(16 << 20)
		return c
	}
	sender := dial("sender")
	defer sender.Close(websocket.StatusNormalClosure, "")
	receiver := dial("receiver")
	defer receiver.Close(websocket.StatusNormalClosure, "")

	// The receiver joining the room triggers presence traffic in real
	// clients; here the room is quiet, so the first read is our payload.
	payload := bytes.Repeat([]byte("block-data-"), 100_000) // ~1.1 MB, 34x the default limit
	if err := sender.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write large message: %v", err)
	}

	_, got, err := receiver.Read(ctx)
	if err != nil {
		t.Fatalf("receive large message (relay likely closed the sender's connection): %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload corrupted in transit: got %d bytes, want %d", len(got), len(payload))
	}

	// The sender must still be connected — a second message goes through.
	if err := sender.Write(ctx, websocket.MessageText, []byte("still-alive")); err != nil {
		t.Fatalf("sender connection did not survive the large message: %v", err)
	}
	_, got, err = receiver.Read(ctx)
	if err != nil || string(got) != "still-alive" {
		t.Fatalf("follow-up message lost: %q err=%v", got, err)
	}
}
