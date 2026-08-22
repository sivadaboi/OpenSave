package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// copyTree mirrors src onto dst, which is what a peer pull does to the peer's
// save folder: it ends up holding exactly what this device held when it asked.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o777); err != nil {
		t.Fatal(err)
	}
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o777)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o666)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// pushed_hash exists to prove, after the fact, that a push landed: the next
// sync looks for the peer holding exactly that hash, and banks it as a
// merge-base because both sides verifiably held it. Migration 0009 puts it
// plainly — "recording the state we handed over".
//
// It does not record the state handed over. It records this device's save as
// it stands at the END of the sync, re-read from disk for lineage purposes and
// reused here. For a game that writes continuously — which is the norm, and
// exactly when a stranded base bites — those are different states, and the one
// stored is one the peer was never given and can never be observed holding.
//
// The consequence is not a wrong answer, it is a repair that never fires: a
// merge-base stranded by a lost convergence report stays stranded, because the
// evidence that would have redeemed it names a state that never existed
// anywhere but here.
func TestPushedHashNamesWhatThePeerReceived(t *testing.T) {
	env := setupEngine(t)

	// Both sides agree to begin with.
	write(t, env.localDir, "slot1.sav", "shared")
	copyTree(t, env.localDir, env.remoteDir)
	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// The user plays. This side now holds newer content, so the next sync is
	// a push: the peer is told to pull, and takes what we hold at that moment.
	write(t, env.localDir, "slot1.sav", "progress-the-peer-receives")

	env.transport.onTriggerPull = func() {
		// The peer pulls, and now holds exactly this.
		copyTree(t, env.localDir, env.remoteDir)
		// The game writes again before this side finishes its sync. Nothing
		// unusual: a save that changes every few seconds is the case the
		// merge-base machinery exists for.
		write(t, env.localDir, "slot1.sav", "written-again-before-the-sync-ended")
	}

	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatalf("push sync: %v", err)
	}
	if env.transport.pullTriggers == 0 {
		t.Fatal("no push happened — the test never exercised the path it is about")
	}

	remote, err := delta.BuildManifest(env.remoteDir)
	if err != nil {
		t.Fatalf("read peer manifest: %v", err)
	}
	local, err := delta.BuildManifest(env.localDir)
	if err != nil {
		t.Fatalf("read local manifest: %v", err)
	}
	pushed := env.store.GetPushedHash("game1", env.peer.ID)

	if pushed == "" {
		t.Fatal("no pushed hash recorded after a push")
	}
	if pushed == remote.ManifestHash() {
		return // correct: it names what the peer actually holds
	}
	if pushed == local.ManifestHash() {
		t.Fatalf("pushed hash names THIS device's post-sync state, which the peer "+
			"never received — so the next sync cannot observe the peer holding it, "+
			"and a merge-base stranded by a lost report stays stranded.\n"+
			"  pushed:      %s (local, after the game wrote again)\n"+
			"  peer holds:  %s (what was actually handed over)",
			pushed[:12], remote.ManifestHash()[:12])
	}
	t.Fatalf("pushed hash matches neither side:\n  pushed %s\n  local  %s\n  peer   %s",
		pushed[:12], local.ManifestHash()[:12], remote.ManifestHash()[:12])
}

// The consequence, end to end: a base stranded by a lost convergence report
// should be repairable from the push record alone. It is not, once the save
// has moved on — so an edit only this device made still reads as a two-way
// divergence.
func TestStrandedBaseIsNotRepairedAfterTheSaveMovesOn(t *testing.T) {
	env := setupEngine(t)

	write(t, env.localDir, "slot1.sav", "shared")
	copyTree(t, env.localDir, env.remoteDir)
	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	write(t, env.localDir, "slot1.sav", "progress-the-peer-receives")
	env.transport.onTriggerPull = func() {
		copyTree(t, env.localDir, env.remoteDir)
		write(t, env.localDir, "slot1.sav", "written-again-before-the-sync-ended")
	}
	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatalf("push sync: %v", err)
	}
	env.transport.onTriggerPull = nil

	// Whatever the engine recorded for that push is the evidence the repair
	// has to work from. Read it rather than manufacturing one, so this test
	// keeps testing the engine and not the test's own idea of the engine.
	recorded := env.store.GetPushedHash("game1", env.peer.ID)
	if recorded == "" {
		t.Fatal("no push record after a push — nothing for the repair to use")
	}

	// The convergence report is lost, so the base stays behind both sides.
	// Injected rather than dropped, because this transport delivers reliably.
	if err := env.store.SetAgreedHash("game1", env.peer.ID, "stale-base-neither-side-holds"); err != nil {
		t.Fatalf("SetAgreedHash: %v", err)
	}
	// SetAgreedHash clears the push record by design. A lost report would not
	// have cleared it — nothing was recorded, which is the whole problem — so
	// put back exactly what the engine had stored.
	if err := env.store.SetPushedHash("game1", env.peer.ID, recorded); err != nil {
		t.Fatalf("SetPushedHash: %v", err)
	}

	// One more edit, on this device only. The peer has not been touched.
	write(t, env.localDir, "slot1.sav", "one-sided-edit")

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Status == "conflict" {
		remote, _ := delta.BuildManifest(env.remoteDir)
		t.Fatalf("a one-sided edit was reported as a conflict: the stranded base was not "+
			"repaired from the push record.\n  recorded push: %s\n  peer holds:    %s\n"+
			"Those must match for the repair to fire — the peer is holding exactly what "+
			"it was handed, and that is a merge-base both sides verifiably held.",
			recorded[:12], remote.ManifestHash()[:12])
	}
}
