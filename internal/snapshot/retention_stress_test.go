package snapshot

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// Retention under concurrent creation.
//
// Pruning reads the snapshot list, decides what is beyond the limits, then
// deletes rows and archives — while other goroutines are inserting. The
// dangerous outcome is not an error but a quiet inconsistency: a row whose
// archive was deleted underneath it (a snapshot that cannot be restored), or
// an archive whose row is gone (disk that is never reclaimed).
//
// The two budgets make this worse than it was, because two independent
// prunes now run over the same list in the same pass.
func TestRetentionUnderConcurrentCreation(t *testing.T) {
	env := setup(t)
	if err := env.store.CreateGame(store.Game{
		ID: "stress", Name: "Stress", SavePath: env.saveDir,
		MaxSnapshots: 3, MaxManualSnapshots: 3,
	}); err != nil {
		t.Fatal(err)
	}
	writeSave(t, env.saveDir, "slot1.sav", "seed")

	const workers = 8
	const each = 4
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				// Alternate the kind so both budgets prune concurrently.
				isAuto := (w+i)%2 == 0
				if _, err := env.mgr.Create("stress", fmt.Sprintf("w%d-%d", w, i), isAuto); err != nil {
					errs <- fmt.Errorf("worker %d create %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent snapshot creation failed: %v", err)
	}

	snaps, err := env.store.ListSnapshots("stress", "main")
	if err != nil {
		t.Fatal(err)
	}

	// Every surviving row must still have its archive: a row without one is a
	// backup the user cannot restore, and nothing reports it until they try.
	autos, manuals := 0, 0
	for _, s := range snaps {
		if _, statErr := os.Stat(s.ZipPath); statErr != nil {
			t.Errorf("surviving snapshot %s has no archive (%s): %v", s.ID, s.ZipPath, statErr)
		}
		if s.IsSystemAuto {
			autos++
		} else {
			manuals++
		}
	}

	// Concurrent prunes may briefly overshoot, but must never leave more than
	// the limit standing once everything has settled.
	if autos > 3 {
		t.Errorf("automatic snapshots kept = %d, want at most 3", autos)
	}
	if manuals > 3 {
		t.Errorf("manual snapshots kept = %d, want at most 3", manuals)
	}
	// And must never prune a kind out of existence: 16 of each were created.
	if autos == 0 || manuals == 0 {
		t.Errorf("a whole kind was pruned away: %d automatic, %d manual", autos, manuals)
	}

	// No staging files left behind. Create stages each archive under a
	// temporary name before claiming an id; a crash or a lost race there
	// would strand them in the backups directory forever.
	entries, err := os.ReadDir(env.backups)
	if err == nil {
		for _, e := range entries {
			if len(e.Name()) > 9 && e.Name()[:9] == ".staging-" {
				t.Errorf("staging file left behind: %s", e.Name())
			}
		}
	}
}

// A final prune pass over a game whose limits changed after the fact must
// converge on the new limits, not the ones in force when the snapshots were
// taken. This is the "clean up now" button's whole job.
func TestPruneAllGamesAppliesCurrentLimits(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "seed")

	game, _ := env.store.GetGame("game1")
	game.MaxSnapshots = 0       // unlimited while we build history up
	game.MaxManualSnapshots = 0 // ditto
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		writeSave(t, env.saveDir, "slot1.sav", fmt.Sprintf("a%d", i))
		if _, err := env.mgr.Create("game1", "", true); err != nil {
			t.Fatal(err)
		}
		writeSave(t, env.saveDir, "slot1.sav", fmt.Sprintf("m%d", i))
		if _, err := env.mgr.Create("game1", "manual", false); err != nil {
			t.Fatal(err)
		}
	}

	// Now tighten both budgets and clean up.
	game, _ = env.store.GetGame("game1")
	game.MaxSnapshots = 2
	game.MaxManualSnapshots = 4
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}
	removed, _, err := env.mgr.PruneAllGames()
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Error("PruneAllGames removed nothing despite both budgets being exceeded")
	}

	snaps, _ := env.store.ListSnapshots("game1", "main")
	autos, manuals := 0, 0
	for _, s := range snaps {
		if s.IsSystemAuto {
			autos++
		} else {
			manuals++
		}
	}
	if autos != 2 {
		t.Errorf("automatic snapshots after prune = %d, want 2", autos)
	}
	if manuals != 4 {
		t.Errorf("manual snapshots after prune = %d, want 4", manuals)
	}
}
