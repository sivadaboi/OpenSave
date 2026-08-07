package store

import "testing"

func lineageStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "elden-ring", Name: "Elden Ring", SavePath: `C:\Saves\ER`}); err != nil {
		t.Fatal(err)
	}
	return s
}

// The primary location must keep using the storage it always used. Every
// device already holds merge bases there, and the rules governing them are
// the ones that took the longest to get right — the root-aware calls have to
// be a pass-through for "", not a reimplementation.
func TestPrimaryRootUsesTheOriginalLineageStorage(t *testing.T) {
	s := lineageStore(t)

	if err := s.SetAgreedHashForRoot("elden-ring", "peer1", "", "hash-a"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAgreedHash("elden-ring", "peer1"); got != "hash-a" {
		t.Errorf("the two-argument reader sees %q; the root-aware writer did not use the same storage", got)
	}

	if err := s.SetAgreedHash("elden-ring", "peer1", "hash-b"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAgreedHashForRoot("elden-ring", "peer1", ""); got != "hash-b" {
		t.Errorf("the root-aware reader sees %q; it did not read the original storage", got)
	}
}

// Locations diverge independently: that is the point of per-root lineage. A
// config folder that disagrees must not drag the save folder's base with it.
func TestEachRootKeepsItsOwnBase(t *testing.T) {
	s := lineageStore(t)

	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "", "primary-hash")
	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "config", "config-hash")
	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "mods", "mods-hash")

	for root, want := range map[string]string{
		"":       "primary-hash",
		"config": "config-hash",
		"mods":   "mods-hash",
	} {
		if got := s.GetAgreedHashForRoot("elden-ring", "peer1", root); got != want {
			t.Errorf("root %q base = %q, want %q", root, got, want)
		}
	}

	// Moving one must not move the others.
	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "config", "config-hash-2")
	if got := s.GetAgreedHashForRoot("elden-ring", "peer1", ""); got != "primary-hash" {
		t.Errorf("advancing the config base moved the primary one to %q", got)
	}
	if got := s.GetAgreedHashForRoot("elden-ring", "peer1", "mods"); got != "mods-hash" {
		t.Errorf("advancing the config base moved the mods one to %q", got)
	}
}

// Recording a convergence settles what the pushed record exists to answer.
// Left behind, a peer rolling back to that exact state would look unchanged
// and get pushed over instead of prompting — the bug the primary path
// already fixes, which the extra roots must not reintroduce.
func TestConvergenceClearsAnExtraRootsPushedHash(t *testing.T) {
	s := lineageStore(t)

	_ = s.SetPushedHashForRoot("elden-ring", "peer1", "config", "pushed-state")
	if got := s.GetPushedHashForRoot("elden-ring", "peer1", "config"); got != "pushed-state" {
		t.Fatalf("pushed hash = %q, want it recorded", got)
	}

	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "config", "newer-agreement")

	if got := s.GetPushedHashForRoot("elden-ring", "peer1", "config"); got != "" {
		t.Errorf("pushed hash survived a later convergence as %q; a peer returning to that state would be silently overwritten", got)
	}
}

// Peers are per-device: one peer's disagreement about a location says
// nothing about another's.
func TestRootLineageIsPerPeer(t *testing.T) {
	s := lineageStore(t)
	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "config", "a")
	_ = s.SetAgreedHashForRoot("elden-ring", "peer2", "config", "b")

	if got := s.GetAgreedHashForRoot("elden-ring", "peer1", "config"); got != "a" {
		t.Errorf("peer1 = %q, want a", got)
	}
	if got := s.GetAgreedHashForRoot("elden-ring", "peer2", "config"); got != "b" {
		t.Errorf("peer2 = %q, want b", got)
	}
}

// A location never synced with this peer has no base, which is what makes
// the first sync a first sync rather than a divergence.
func TestUnknownRootHasNoBase(t *testing.T) {
	s := lineageStore(t)
	if got := s.GetAgreedHashForRoot("elden-ring", "peer1", "never-synced"); got != "" {
		t.Errorf("an unsynced location reports base %q, want empty", got)
	}
}

// Removing a location drops its lineage, so re-adding the same name later
// starts clean instead of inheriting a base describing files now gone.
func TestRemovingARootForgetsItsLineage(t *testing.T) {
	s := lineageStore(t)
	_ = s.AddGameRoot("elden-ring", "config", `C:\Docs\ER`)
	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "config", "stale-base")

	if err := s.RemoveGameRoot("elden-ring", "config"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAgreedHashForRoot("elden-ring", "peer1", "config"); got != "" {
		t.Errorf("base %q survived the location being removed", got)
	}
}

// Unpairing forgets the device entirely, extra locations included.
func TestUnpairingClearsRootLineage(t *testing.T) {
	s := lineageStore(t)
	if err := s.UpsertPeer(Peer{ID: "peer1", Name: "Deck", Address: "10.0.0.5", Port: 8383}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetAgreedHashForRoot("elden-ring", "peer1", "config", "base")

	if err := s.UnpairPeer("peer1"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetAgreedHashForRoot("elden-ring", "peer1", "config"); got != "" {
		t.Errorf("root lineage %q outlived the peer being unpaired", got)
	}
}
