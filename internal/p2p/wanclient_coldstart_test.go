package p2p

import (
	"testing"
	"time"
)

// reportThreshold is the decision connectionLost makes, isolated so it can be
// exercised without a relay to dial: how long the relay must stay away before
// the user is told.
func (w *WanClient) reportThreshold() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.connectedTo != w.targetURL {
		return wanColdStartGrace
	}
	return wanOutageReportAfter
}

// A relay that has never answered yet gets the longer grace period. Render's
// free tier takes the better part of a minute to wake, and the backoff has
// had three goes by 45s — so the shorter threshold announces a routine
// wake-up as a fault seconds before the connection succeeds.
func TestColdStartGetsTheLongerGrace(t *testing.T) {
	w, _, _ := newPresenceTestClient(t)

	w.mu.Lock()
	w.targetURL = "wss://relay.example"
	w.mu.Unlock()

	if got := w.reportThreshold(); got != wanColdStartGrace {
		t.Errorf("before any successful connection the threshold = %s, want the cold-start grace %s", got, wanColdStartGrace)
	}

	// Once the relay has answered, a drop means something actually changed.
	w.mu.Lock()
	w.connectedTo = "wss://relay.example"
	w.mu.Unlock()

	if got := w.reportThreshold(); got != wanOutageReportAfter {
		t.Errorf("after connecting the threshold = %s, want %s", got, wanOutageReportAfter)
	}
}

// Changing the relay in settings points this client at a host it has never
// reached, which may equally be asleep. A plain "have we ever connected"
// flag would stay true and hand the new relay the short threshold.
func TestSwitchingRelayEarnsTheGraceAgain(t *testing.T) {
	w, _, _ := newPresenceTestClient(t)

	w.mu.Lock()
	w.targetURL = "wss://old.example"
	w.connectedTo = "wss://old.example"
	w.mu.Unlock()

	if got := w.reportThreshold(); got != wanOutageReportAfter {
		t.Fatalf("connected to the current relay: threshold = %s, want %s", got, wanOutageReportAfter)
	}

	w.mu.Lock()
	w.targetURL = "wss://new.example" // settings changed; Connect() re-runs
	w.mu.Unlock()

	if got := w.reportThreshold(); got != wanColdStartGrace {
		t.Errorf("dialling a relay never reached: threshold = %s, want the cold-start grace %s", got, wanColdStartGrace)
	}
}

// Leaving the room clears what we had reached, so rejoining is a cold start
// again rather than inheriting the last session's state.
func TestDisconnectForgetsTheReachedRelay(t *testing.T) {
	w, _, _ := newPresenceTestClient(t)

	w.mu.Lock()
	w.targetURL = "wss://relay.example"
	w.connectedTo = "wss://relay.example"
	w.mu.Unlock()

	w.Disconnect()

	if got := w.reportThreshold(); got != wanColdStartGrace {
		t.Errorf("after Disconnect the threshold = %s, want the cold-start grace %s", got, wanColdStartGrace)
	}
}

// The grace has to outlast the backoff's early attempts, or it changes
// nothing: by 45s the client has typically failed three times, which is the
// whole reason a waking relay reads as an outage today.
func TestGraceOutlastsTheEarlyBackoff(t *testing.T) {
	if wanColdStartGrace <= wanOutageReportAfter {
		t.Fatalf("cold-start grace %s must exceed the ordinary threshold %s", wanColdStartGrace, wanOutageReportAfter)
	}
	// Sum the delays the client waits through before the grace expires.
	var elapsed time.Duration
	attempts := 0
	for elapsed < wanColdStartGrace {
		elapsed += reconnectDelay(attempts)
		attempts++
		if attempts > 20 {
			break
		}
	}
	if attempts < 4 {
		t.Errorf("only %d reconnect attempts fit inside the %s grace; a waking relay needs more room than that",
			attempts, wanColdStartGrace)
	}
}
