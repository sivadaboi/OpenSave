package p2p

import "testing"

// One missed ping is not evidence that a device has gone away.
//
// A ping is a 3-second HTTP round trip, and a busy machine, a wifi blip or a
// laptop that suspended for a moment all produce a failed one. Marking a peer
// offline on the first miss meant a device sitting on the same desk was
// reported gone, and any sync started in that window failed with "no online
// peers available" — which is what has been turning the CI race job red, a
// different sync test each run depending on which one landed in the gap.
func TestOfflineStrikes_OneMissIsNotEnough(t *testing.T) {
	e := &Engine{}

	if got := e.notePingMiss("peer1"); got >= offlineStrikes {
		t.Fatalf("a single miss counted %d, which already declares the peer offline", got)
	}
	if got := e.notePingMiss("peer1"); got >= offlineStrikes {
		t.Fatalf("two misses counted %d, still too eager", got)
	}
	if got := e.notePingMiss("peer1"); got < offlineStrikes {
		t.Errorf("three misses counted %d; a device that really has gone must be noticed", got)
	}
}

// Recovery is not rationed: one answer restores the peer, so a device that
// blinks does not spend the next several probes marked offline.
func TestOfflineStrikes_OneAnswerClearsTheRecord(t *testing.T) {
	e := &Engine{}

	e.notePingMiss("peer1")
	e.notePingMiss("peer1")
	e.clearPingMisses("peer1")

	if got := e.notePingMiss("peer1"); got != 1 {
		t.Errorf("after answering, the next miss counted %d; the old failures should be forgotten", got)
	}
}

// Peers are counted separately, or one unreachable device would evict the
// others alongside it.
func TestOfflineStrikes_CountedPerPeer(t *testing.T) {
	e := &Engine{}

	for i := 0; i < offlineStrikes; i++ {
		e.notePingMiss("gone")
	}
	if got := e.notePingMiss("present"); got != 1 {
		t.Errorf("a second peer's first miss counted %d; the counters are shared", got)
	}
}

// The zero Engine must work: the map is created on first use, like the other
// per-peer caches on this type.
func TestOfflineStrikes_NoInitialisationNeeded(t *testing.T) {
	e := &Engine{}
	if got := e.notePingMiss("peer1"); got != 1 {
		t.Errorf("first miss on a fresh engine counted %d, want 1", got)
	}
	e.clearPingMisses("never-seen") // must not panic on an absent key
}

// Three is a deliberate value: it has to be more than one, or the change does
// nothing, and small enough that a device that has really gone is noticed
// quickly rather than lingering as online while syncs are attempted against it.
func TestOfflineStrikes_IsMoreThanOneAndStillPrompt(t *testing.T) {
	if offlineStrikes < 2 {
		t.Errorf("offlineStrikes = %d: one miss still declares a peer offline", offlineStrikes)
	}
	if offlineStrikes > 5 {
		t.Errorf("offlineStrikes = %d: a departed device stays online too long", offlineStrikes)
	}
}
