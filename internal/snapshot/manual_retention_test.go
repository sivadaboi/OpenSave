package snapshot

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// isIDCollision decides whether claimAndInsert walks forward to the next
// millisecond or gives up and reports the real failure. Getting it wrong is
// silent in opposite directions: too narrow and a genuine collision aborts a
// backup, too broad and a closed database or a full disk is retried a
// thousand times and then reported as "could not find a free snapshot id",
// which sends the reader after entirely the wrong problem.
//
// The concurrency test cannot reach this: in-process claims are serialised by
// a mutex and the id is checked free before the insert, so the insert only
// ever collides with a SECOND PROCESS. The matched text is therefore checked
// directly against what the driver really produces.
func TestIsIDCollisionMatchesTheRealDriverError(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "x")

	snap, err := env.mgr.Create("game1", "first", false)
	if err != nil {
		t.Fatal(err)
	}
	// Insert the same id again: exactly what a second process racing for the
	// same millisecond hits.
	dupErr := env.store.CreateSnapshot(store.Snapshot{
		ID: snap.ID, GameID: "game1", BranchName: "main", Timestamp: snap.Timestamp,
	})
	if dupErr == nil {
		t.Fatal("re-inserting an existing snapshot id succeeded; the id is not unique")
	}
	if !isIDCollision(dupErr) {
		t.Errorf("a real duplicate-id error is not recognised as a collision, so a "+
			"racing process would abort the backup instead of retrying: %v", dupErr)
	}

	// And the errors that must NOT be retried.
	for _, other := range []error{
		errors.New("database is closed"),
		errors.New("disk I/O error"),
		fmt.Errorf("create snapshot x: %w", errors.New("no such table: snapshots")),
		nil,
	} {
		if isIDCollision(other) {
			t.Errorf("%v is treated as an id collision, so it would be retried 1000 "+
				"times and then misreported as a missing free id", other)
		}
	}
}

// The reported failure, exactly: a game that auto-saves constantly evicts the
// snapshots its user took on purpose.
//
// Elden Ring and Dragonsword: Awakening write their save every few minutes,
// so the watcher fills the whole retention budget during a single session.
// Under one shared budget the automatic snapshots are always the newest, so
// "keep the newest N" deletes the deliberate ones first — the snapshot taken
// before a boss is gone by the time it is wanted, which makes the button
// pointless precisely when it matters.
func TestManualSnapshotsSurviveAnAutoSaveFlood(t *testing.T) {
	env := setup(t) // maxSnapshots = 3, maxManualSnapshots = 0 (keep forever)
	writeSave(t, env.saveDir, "slot1.sav", "start")

	manual, err := env.mgr.Create("game1", "before the boss", false)
	if err != nil {
		t.Fatal(err)
	}

	// A play session's worth of automatic snapshots — far more than the
	// retention limit, which is the whole point.
	for i := 0; i < 10; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "autosave")
		if _, err := env.mgr.Create("game1", "", true); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := env.store.GetSnapshot(manual.ID); err != nil {
		t.Fatalf("the manual snapshot %q was pruned by automatic ones: %v", manual.ID, err)
	}
	if _, err := os.Stat(manual.ZipPath); err != nil {
		t.Errorf("the manual snapshot's archive was deleted from disk: %v", err)
	}

	// The automatic ones must still be held to their own limit, or this
	// "fix" is just an unbounded disk leak.
	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	autos := 0
	for _, s := range snaps {
		if s.IsSystemAuto {
			autos++
		}
	}
	if autos != 3 {
		t.Errorf("automatic snapshots kept = %d, want 3 (their own limit still applies)", autos)
	}
}

// Manual snapshots can be capped too, for people who would rather bound the
// disk than keep everything.
func TestManualSnapshotsHonourTheirOwnLimit(t *testing.T) {
	env := setup(t)
	game, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	game.MaxManualSnapshots = 2
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}

	var manual []store.Snapshot
	for i := 0; i < 5; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "manual")
		snap, err := env.mgr.Create("game1", "checkpoint", false)
		if err != nil {
			t.Fatal(err)
		}
		manual = append(manual, snap)
	}

	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, s := range snaps {
		if !s.IsSystemAuto {
			kept++
		}
	}
	if kept != 2 {
		t.Errorf("manual snapshots kept = %d, want 2", kept)
	}
	// Oldest-first pruning: the three earliest must be the ones gone.
	for _, gone := range manual[:3] {
		if _, err := env.store.GetSnapshot(gone.ID); err == nil {
			t.Errorf("manual snapshot %q should have been pruned as the oldest", gone.ID)
		}
	}
	for _, alive := range manual[3:] {
		if _, err := env.store.GetSnapshot(alive.ID); err != nil {
			t.Errorf("manual snapshot %q should have been kept: %v", alive.ID, err)
		}
	}
}

// A manual budget must not drag automatic snapshots down with it, and an
// automatic budget of "unlimited" must not disable manual pruning — the two
// limits are independent in both directions.
func TestTheTwoBudgetsAreIndependent(t *testing.T) {
	env := setup(t)
	game, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	game.MaxSnapshots = 0 // keep every automatic snapshot
	game.MaxManualSnapshots = 1
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "auto")
		if _, err := env.mgr.Create("game1", "", true); err != nil {
			t.Fatal(err)
		}
		writeSave(t, env.saveDir, "slot1.sav", "manual")
		if _, err := env.mgr.Create("game1", "checkpoint", false); err != nil {
			t.Fatal(err)
		}
	}

	snaps, err := env.store.ListSnapshots("game1", "main")
	if err != nil {
		t.Fatal(err)
	}
	autos, manuals := 0, 0
	for _, s := range snaps {
		if s.IsSystemAuto {
			autos++
		} else {
			manuals++
		}
	}
	if autos != 4 {
		t.Errorf("automatic snapshots = %d, want all 4 kept (limit 0 = unlimited)", autos)
	}
	if manuals != 1 {
		t.Errorf("manual snapshots = %d, want 1 (their own limit)", manuals)
	}
}

// Existing installs upgrade into "keep manual forever" without being asked:
// the migration defaults the new column to 0, and a game carried over from
// before the split must not keep losing snapshots after the update.
func TestExistingGamesDefaultToKeepingManualSnapshots(t *testing.T) {
	env := setup(t)
	game, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	if game.MaxManualSnapshots != 0 {
		t.Fatalf("a game created without the field set has MaxManualSnapshots = %d, want 0",
			game.MaxManualSnapshots)
	}

	writeSave(t, env.saveDir, "slot1.sav", "start")
	var manual []store.Snapshot
	for i := 0; i < 6; i++ {
		writeSave(t, env.saveDir, "slot1.sav", "manual")
		snap, err := env.mgr.Create("game1", "checkpoint", false)
		if err != nil {
			t.Fatal(err)
		}
		manual = append(manual, snap)
	}
	for _, s := range manual {
		if _, err := env.store.GetSnapshot(s.ID); err != nil {
			t.Errorf("manual snapshot %q was pruned under the default settings: %v", s.ID, err)
		}
	}
}
