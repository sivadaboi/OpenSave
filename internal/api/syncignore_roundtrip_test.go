package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// Exclusion rules saved fine and then appeared to vanish: reopening a game's
// Configuration showed an empty box, because the payload the UI reloads from
// was a hand-built map that never included the field. The rules were in the
// database the whole time and were being applied — only the screen disagreed,
// which is the worst shape for this bug to take. The natural response is to
// retype them, and every re-save writes the same correct value while still
// looking like it failed.
//
// Reported on 2.3.0 (SteamOS), the release that introduced the feature.
func TestSyncIgnoreSurvivesAReload(t *testing.T) {
	ts := startTestServer(t)

	if err := os.WriteFile(filepath.Join(ts.saveDir, "slot1.sav"), []byte("progress"), 0o666); err != nil {
		t.Fatal(err)
	}
	resp, body := ts.do(t, http.MethodPost, "/api/games", map[string]string{
		"name": "Ignore Game", "savePath": ts.saveDir,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("track status = %d (%v)", resp.StatusCode, body)
	}
	var gameID string
	if err := json.Unmarshal(body["id"], &gameID); err != nil {
		t.Fatal(err)
	}

	const rules = "settings.ini\n*.log\n/cache/"

	resp, body = ts.do(t, http.MethodPatch, "/api/games/"+gameID, map[string]any{
		"syncIgnore": rules,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status = %d (%v)", resp.StatusCode, body)
	}

	// What the screen reads when it is reopened.
	resp, listed := ts.do(t, http.MethodGet, "/api/games", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	raw, ok := listed[gameID]
	if !ok {
		t.Fatalf("game %q missing from the list", gameID)
	}
	var reloaded map[string]any
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}

	got, present := reloaded["syncIgnore"]
	if !present {
		t.Fatal("syncIgnore is absent from the games payload — the rules are stored but never " +
			"sent back, so reopening Configuration shows an empty box")
	}
	if got != rules {
		t.Errorf("syncIgnore = %q, want %q", got, rules)
	}

	// And it really is in the database, so a failure above is about the wire
	// shape rather than the save.
	g, err := ts.daemon.Store.GetGame(gameID)
	if err != nil {
		t.Fatal(err)
	}
	if g.SyncIgnore != rules {
		t.Errorf("stored syncIgnore = %q, want %q", g.SyncIgnore, rules)
	}
}

// The payload is written out by hand, so a field added to the model reaches
// the UI only if someone remembers this map too. Nothing connected the two,
// and syncIgnore was the first field to be forgotten. This fails when the
// next one is.
func TestGamePayloadCarriesEveryModelField(t *testing.T) {
	ts := startTestServer(t)

	payload := ts.server.gamePayload(store.Game{ID: "g", Name: "G"})

	// json tags on store.Game, minus the ones deliberately not on the wire.
	rt := reflect.TypeOf(store.Game{})
	var missing []string
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, ok := payload[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("gamePayload omits model fields %v — anything the UI loads from this payload "+
			"will read as empty, however correctly it was saved", missing)
	}
}
