package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// Setting an App ID by hand exists for the games automatic detection cannot
// reach: a save under AppData\<company>\<game> has no App ID anywhere in its
// path, and the bundled manifest does not know every title. Without a way to
// supply it, App-ID matching is unavailable for exactly the games that need
// it — which is what a user hit on 2.2.0.
//
// So the test is not "does the field save". It is whether a hand-set ID makes
// two differently-named copies actually sync.
func TestManualAppID_DrivesCrossDeviceMatching(t *testing.T) {
	a := testutil.NewTestDaemon(t, "ManualAppID-A")
	b := testutil.NewTestDaemon(t, "ManualAppID-B")
	a.PairWith(b)

	// Both devices opt in to App-ID matching; it is off by default.
	a.API(http.MethodPost, "/api/settings", map[string]any{"matchByAppId": true}, nil)
	b.API(http.MethodPost, "/api/settings", map[string]any{"matchByAppId": true}, nil)

	// The same game, tracked under names that share nothing.
	a.WriteSave("slot1.sav", "from-A")
	gameA := a.TrackGame("Mina The Howler")

	var gameB struct {
		ID string `json:"id"`
	}
	b.API(http.MethodPost, "/api/games",
		map[string]string{"name": "MTH.Repack.Final", "savePath": b.SaveDir}, &gameB)
	if gameB.ID == "" {
		t.Fatal("tracking on the peer returned no id")
	}

	const appID = "9999001"
	a.API(http.MethodPatch, "/api/games/"+gameA, map[string]string{"appId": appID}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameB.ID, map[string]string{"appId": appID}, nil)

	// It has to persist and read back, or the UI would show it reverting.
	var readBack map[string]struct {
		AppID string `json:"appId"`
	}
	a.API(http.MethodGet, "/api/games", nil, &readBack)
	if got := readBack[gameA].AppID; got != appID {
		t.Fatalf("app id read back as %q, want %q", got, appID)
	}

	// The payoff: two names with nothing in common now sync, because the
	// shared App ID is what resolves the peer's game to the local one.
	a.API(http.MethodPost, "/api/games/"+gameA+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "from-A"
	}) {
		t.Errorf("the save never reached the peer: b slot1=%q — a hand-set App ID "+
			"did not match the two entries", b.ReadSave("slot1.sav"))
	}
}

// Matching is opt-in, and a hand-set App ID must not quietly bypass that.
// Two copies of a game sharing an ID is not consent to merge them.
func TestManualAppID_RespectsMatchingBeingOff(t *testing.T) {
	a := testutil.NewTestDaemon(t, "ManualAppIDOff-A")
	b := testutil.NewTestDaemon(t, "ManualAppIDOff-B")
	a.PairWith(b)

	a.WriteSave("slot1.sav", "from-A")
	gameA := a.TrackGame("Some Game")

	var gameB struct {
		ID string `json:"id"`
	}
	b.API(http.MethodPost, "/api/games",
		map[string]string{"name": "Totally.Different.Name", "savePath": b.SaveDir}, &gameB)

	const appID = "9999002"
	a.API(http.MethodPatch, "/api/games/"+gameA, map[string]string{"appId": appID}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameB.ID, map[string]string{"appId": appID}, nil)

	a.API(http.MethodPost, "/api/games/"+gameA+"/sync", nil, nil)

	// Give it long enough that a sync would have happened if it were going to.
	if testutil.WaitFor(6*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "from-A"
	}) {
		t.Error("a shared App ID merged two games while App-ID matching was off — " +
			"the setting is what stops two separate copies being treated as one")
	}
}
