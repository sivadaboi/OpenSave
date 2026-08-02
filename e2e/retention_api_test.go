package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// The contract the desktop UI depends on for the manual-snapshot budget.
//
// The Configuration tab reads maxManualSnapshots off the game payload and
// PATCHes it back with the rest of the form. Nothing in the Svelte build
// checks either field name — a rename on the Go side would leave the input
// silently bound to undefined, save 0 over whatever the user had, and look
// like it worked. This pins both directions.
func TestRetentionAPI_ManualBudgetRoundTripsThroughTheGamePayload(t *testing.T) {
	a := testutil.NewTestDaemon(t, "RetAPI")
	a.WriteSave("slot1.sav", "x")
	gameID := a.TrackGame("Retention API")

	type gamePayload struct {
		ID                 string `json:"id"`
		MaxSnapshots       int    `json:"maxSnapshots"`
		MaxManualSnapshots int    `json:"maxManualSnapshots"`
		AutoSync           bool   `json:"autoSync"`
	}

	// The field must be present and default to "keep forever".
	var initial gamePayload
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{}, &initial)
	if initial.MaxManualSnapshots != 0 {
		t.Errorf("a new game reports maxManualSnapshots = %d, want 0 (keep forever)",
			initial.MaxManualSnapshots)
	}

	// The UI sends the whole form back, so send both limits together.
	var saved gamePayload
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{
		"maxSnapshots":       5,
		"maxManualSnapshots": 9,
		"autoSync":           true,
	}, &saved)
	if saved.MaxManualSnapshots != 9 {
		t.Errorf("PATCH returned maxManualSnapshots = %d, want 9", saved.MaxManualSnapshots)
	}
	if saved.MaxSnapshots != 5 {
		t.Errorf("PATCH returned maxSnapshots = %d, want 5", saved.MaxSnapshots)
	}

	// And it must survive a re-read, not just be echoed back.
	var reread gamePayload
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{}, &reread)
	if reread.MaxManualSnapshots != 9 {
		t.Errorf("after re-reading, maxManualSnapshots = %d, want 9 — the value was echoed but not stored",
			reread.MaxManualSnapshots)
	}

	// A PATCH that omits the field must not silently reset it. The UI always
	// sends it, but the Decky plugin and any external tooling do not.
	var partial gamePayload
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": true}, &partial)
	if partial.MaxManualSnapshots != 9 {
		t.Errorf("a PATCH that omitted maxManualSnapshots reset it to %d",
			partial.MaxManualSnapshots)
	}
}

// The global default lives in Settings and is what newly tracked games
// inherit. Same reasoning: the Settings screen clones the whole payload, so
// the field has to survive a settings round trip.
func TestRetentionAPI_GlobalManualDefaultRoundTrips(t *testing.T) {
	a := testutil.NewTestDaemon(t, "RetAPIDefault")

	type settingsPayload struct {
		DefaultMaxSnapshots       int `json:"defaultMaxSnapshots"`
		DefaultMaxManualSnapshots int `json:"defaultMaxManualSnapshots"`
	}

	var before settingsPayload
	a.API(http.MethodGet, "/api/settings", nil, &before)
	if before.DefaultMaxManualSnapshots != 0 {
		t.Errorf("the default manual budget starts at %d, want 0 (keep forever)",
			before.DefaultMaxManualSnapshots)
	}

	var saved settingsPayload
	a.API(http.MethodPost, "/api/settings", map[string]any{
		"defaultMaxManualSnapshots": 6,
	}, &saved)
	if saved.DefaultMaxManualSnapshots != 6 {
		t.Errorf("saving the default returned %d, want 6", saved.DefaultMaxManualSnapshots)
	}

	var reread settingsPayload
	a.API(http.MethodGet, "/api/settings", nil, &reread)
	if reread.DefaultMaxManualSnapshots != 6 {
		t.Errorf("after re-reading, the default is %d, want 6", reread.DefaultMaxManualSnapshots)
	}

	// A game tracked after the change inherits it.
	a.WriteSave("slot1.sav", "y")
	gameID := a.TrackGame("Inheriting Game")
	if !testutil.WaitFor(15*time.Second, func() bool {
		var g struct {
			MaxManualSnapshots int `json:"maxManualSnapshots"`
		}
		a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{}, &g)
		return g.MaxManualSnapshots == 6
	}) {
		var g struct {
			MaxManualSnapshots int `json:"maxManualSnapshots"`
		}
		a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{}, &g)
		t.Errorf("a newly tracked game got maxManualSnapshots = %d, want the global default 6",
			g.MaxManualSnapshots)
	}
}
