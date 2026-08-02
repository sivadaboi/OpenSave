package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// Cloud retention mirrors the local automatic limit, and must mirror the
// exemption too.
//
// Every upload prunes the game's remote copies down to maxSnapshots. Applied
// to every file regardless of kind, that deletes the user's deliberate
// snapshots from the cloud even though local retention now keeps them —
// leaving the backup thinner than the machine it is backing up, and doing it
// silently, in the one place the user cannot see.
func TestCloudRetention_ManualSnapshotsAreNotPrunedFromTheCloud(t *testing.T) {
	a := testutil.NewTestDaemon(t, "CloudManual")
	cloudDir := useLocalCloud(t, a)

	a.WriteSave("slot1.sav", "start")
	gameID := a.TrackGame("Cloud Manual")

	// A tight automatic budget, so uploads prune aggressively.
	a.API(http.MethodPatch, "/api/games/"+gameID,
		map[string]any{"maxSnapshots": 2, "maxManualSnapshots": 0}, nil)

	// One deliberate snapshot, taken before everything else.
	var manual struct {
		ID string `json:"id"`
	}
	a.WriteSave("slot1.sav", "the state worth keeping")
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "BEFORE THE BOSS"}, &manual)
	if manual.ID == "" {
		t.Fatal("could not take the manual snapshot")
	}
	waitForUpload(t, cloudDir)

	// Then a run of ordinary snapshots, each of which triggers cloud pruning.
	for i := 0; i < 5; i++ {
		a.WriteSave("slot1.sav", "later state")
		a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
			map[string]string{"comment": "routine"}, nil)
		time.Sleep(300 * time.Millisecond)
	}

	// Let the uploads and their pruning settle, then look at what is left.
	deadline := time.Now().Add(20 * time.Second)
	var names []string
	for time.Now().Before(deadline) {
		names = cloudFiles(t, cloudDir)
		time.Sleep(500 * time.Millisecond)
		if sameStringSet(names, cloudFiles(t, cloudDir)) {
			break // stopped changing
		}
	}

	found := false
	for _, n := range names {
		if strings.Contains(n, manual.ID) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the manual snapshot %s was pruned from the cloud by routine uploads.\n"+
			"  remaining: %v", manual.ID, names)
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
