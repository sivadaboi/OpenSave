package store

import (
	"testing"
)

func TestPushedHashRoundTrip(t *testing.T) {
	s := openTestStore(t)

	if got := s.GetPushedHash("game", "peer"); got != "" {
		t.Errorf("a game/peer with no row returns %q, want empty", got)
	}
	if err := s.SetPushedHash("game", "peer", "abc123"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetPushedHash("game", "peer"); got != "abc123" {
		t.Errorf("GetPushedHash = %q, want %q", got, "abc123")
	}
}

// The push record must not outlive the push. Recording any convergence
// settles the question it exists to answer, so it is cleared at the same
// time — otherwise a later pull moves the base on, the stale record survives,
// and if the peer ever returns to the older pushed state it would drag the
// base backwards onto it and skip a conflict that should have been raised.
func TestSetAgreedHashClearsThePushRecord(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetPushedHash("game", "peer", "pushed-state"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgreedHash("game", "peer", "converged-state"); err != nil {
		t.Fatal(err)
	}

	if got := s.GetPushedHash("game", "peer"); got != "" {
		t.Errorf("the push record survived a recorded convergence: %q", got)
	}
	if got := s.GetAgreedHash("game", "peer"); got != "converged-state" {
		t.Errorf("GetAgreedHash = %q, want %q", got, "converged-state")
	}
}

// The two records are per game AND per peer: a push to one device must not
// be readable as a push to another, or a two-peer setup would repair one
// peer's merge-base using evidence from the other.
func TestPushedHashIsPerGameAndPeer(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetPushedHash("game1", "peerA", "hash-A"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPushedHash("game1", "peerB", "hash-B"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPushedHash("game2", "peerA", "hash-C"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ game, peer, want string }{
		{"game1", "peerA", "hash-A"},
		{"game1", "peerB", "hash-B"},
		{"game2", "peerA", "hash-C"},
		{"game2", "peerB", ""},
	} {
		if got := s.GetPushedHash(tc.game, tc.peer); got != tc.want {
			t.Errorf("GetPushedHash(%q, %q) = %q, want %q", tc.game, tc.peer, got, tc.want)
		}
	}

	// Clearing one pair must leave the others alone.
	if err := s.SetAgreedHash("game1", "peerA", "converged"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetPushedHash("game1", "peerB"); got != "hash-B" {
		t.Errorf("converging with peerA cleared peerB's record: %q", got)
	}
	if got := s.GetPushedHash("game2", "peerA"); got != "hash-C" {
		t.Errorf("converging on game1 cleared game2's record: %q", got)
	}
}
