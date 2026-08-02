package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// Deleting many files at once, which is what clearing a save folder or
// switching a game's layout looks like.
//
// Each deletion arrives on its own and each one leaves the receiver's
// merge-base needing to be re-derived. Firing a manifest fetch per file would
// mean a hundred concurrent fetches of the same peer over the relay, so they
// are coalesced — and coalescing is where this gets subtle: a refresh already
// in flight may have read the peer's manifest BEFORE the last deletions were
// applied, so simply dropping the later requests leaves the base describing
// files that are already gone. The final state has to win.
func TestDeletionMultiFile_TheBaseReflectsTheLastDeletionNotTheFirst(t *testing.T) {
	files := map[string]string{"keep.sav": "constant"}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("doomed%02d.sav", i)] = "delete me"
	}
	a, b, gameID := trackBothOverRelay(t, "DelMulti", files)

	time.Sleep(syncSettleWindow)
	if !testutil.WaitFor(30*time.Second, func() bool {
		return a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()) != ""
	}) {
		t.Fatal("no merge-base recorded after the initial sync")
	}
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)

	// B removes them all and propagates in one pass.
	for i := 0; i < 12; i++ {
		b.RemoveSave(fmt.Sprintf("doomed%02d.sav", i))
	}
	syncTo(b, gameID, a.NodeID())

	if !testutil.WaitFor(60*time.Second, func() bool {
		for i := 0; i < 12; i++ {
			if a.ReadSave(fmt.Sprintf("doomed%02d.sav", i)) != "" {
				return false
			}
		}
		return true
	}) {
		t.Fatal("not every deletion reached A")
	}

	// The base must describe what A holds NOW — all twelve gone — not the
	// state as of whichever deletion happened to be in flight first.
	if !testutil.WaitFor(30*time.Second, func() bool {
		return a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()) == peerHeldHash(t, a, gameID)
	}) {
		t.Errorf("after a twelve-file deletion the merge-base is %.12q but A holds %.12q — "+
			"a coalesced refresh settled on an intermediate state",
			a.Daemon.Store.GetAgreedHash(gameID, b.NodeID()), peerHeldHash(t, a, gameID))
	}

	// And the user-visible consequence must be absent.
	a.WriteSave("keep.sav", "edited by A alone")
	if status, _ := syncTo(a, gameID, b.NodeID()); status == "conflict" {
		ca, _ := conflictOn(a, gameID)
		t.Errorf("a one-sided edit after a multi-file deletion conflicted: %+v", ca.DiffFiles)
	}
}
