package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/p2p"
	"github.com/opensave/opensave/testutil"
)

// A game whose save is split across two folders — the pattern behind the
// request this feature came from: save data in one place, configuration in
// another, both belonging to one title.
//
// These run two real daemons over real HTTP. The unit tests prove each piece
// behaves; only this proves the pieces fit.

// extraDir makes a second save folder for a daemon and returns its path.
func extraDir(t *testing.T, td *testutil.TestDaemon, name string) string {
	t.Helper()
	dir := filepath.Join(testutil.TempDir(t), name)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeIn(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

func readIn(dir, rel string) string {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return string(b)
}

// addRoot attaches an extra save location through the API, the way the app
// will.
func addRoot(t *testing.T, td *testutil.TestDaemon, gameID, name, path string) {
	t.Helper()
	td.API(http.MethodPost, "/api/games/"+gameID+"/roots",
		map[string]string{"name": name, "path": path}, nil)
}

// Two devices, one game, two folders each. A file written to the second
// folder on A must arrive in the second folder on B — and specifically NOT
// in B's save folder, which is the failure this whole design exists to
// prevent.
func TestMultiRoot_SecondFolderSyncsToTheSecondFolder(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "MultiRoot", map[string]string{
		"save.sav": "primary data",
	})

	aConfig := extraDir(t, a, "config")
	bConfig := extraDir(t, b, "config")
	writeIn(t, aConfig, "settings.ini", "fullscreen=1")

	addRoot(t, a, gameID, "config", aConfig)
	addRoot(t, b, gameID, "config", bConfig)

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "settings.ini") == "fullscreen=1"
	}) {
		t.Fatalf("the second folder never reached the peer: config=%q", readIn(bConfig, "settings.ini"))
	}

	// The critical negative: it must not have landed in the save folder.
	if got := b.ReadSave("settings.ini"); got != "" {
		t.Errorf("the config file was written into the save folder as %q — locations are not being kept apart", got)
	}
	if got := b.ReadSave("config/settings.ini"); got != "" {
		t.Error("the config file was written as a subfolder of the save folder")
	}
	// And the primary save is untouched by any of it.
	if got := b.ReadSave("save.sav"); got != "primary data" {
		t.Errorf("primary save = %q, want it unaffected", got)
	}
}

// The realistic mixed-version case: B is on 2.2.1, so it answers without a
// proto AND has no extra locations of its own, because that build has no way
// to configure any. A has two locations; B must receive the primary save
// normally and nothing else.
//
// Note what this does and does not prove. Either exclusion alone is enough
// here — B reports no proto, and B lists no locations — so this pins the
// behaviour a real pair of devices will show, not the capability gate
// specifically. TestMultiRoot_ProtoGateAloneStopsExtraLocations isolates the
// gate.
func TestMultiRoot_OlderPeerGetsThePrimarySaveAndNothingElse(t *testing.T) {
	// B answers like a build that predates save locations.
	restore := p2p.SetServedProto(0)
	defer p2p.SetServedProto(restore)

	a := testutil.NewTestDaemon(t, "MixedVer-A")
	b := testutil.NewTestDaemon(t, "MixedVer-B")
	a.PairWith(b)

	a.WriteSave("save.sav", "primary data")
	gameID := a.TrackGame("MixedVer")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "MixedVer", "savePath": b.SaveDir}, nil)

	aConfig := extraDir(t, a, "config")
	writeIn(t, aConfig, "settings.ini", "fullscreen=1")
	addRoot(t, a, gameID, "config", aConfig)

	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	// The primary save still syncs: an older peer loses the extra locations,
	// not the game.
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("save.sav") == "primary data"
	}) {
		t.Fatalf("the primary save never reached the older peer: %q", b.ReadSave("save.sav"))
	}

	// Give any (incorrect) extra-location traffic time to land before
	// asserting it did not.
	time.Sleep(syncSettleWindow)

	for _, rel := range []string{"settings.ini", "config/settings.ini", "config"} {
		if got := b.ReadSave(rel); got != "" {
			t.Errorf("an older peer received extra-location content at %q (%q); it cannot name locations and would misplace them", rel, got)
		}
	}
	if _, err := os.Stat(filepath.Join(b.SaveDir, "config")); err == nil {
		t.Error("a folder named after an extra location was created inside the older peer's save folder")
	}
}

// A location B has never been told about is not something A may invent a
// path for. B must end up with the primary save and no second folder
// conjured anywhere.
func TestMultiRoot_PeerWithoutTheLocationIsNotGivenOne(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "OneSided", map[string]string{
		"save.sav": "primary data",
	})

	aConfig := extraDir(t, a, "config")
	writeIn(t, aConfig, "settings.ini", "fullscreen=1")
	addRoot(t, a, gameID, "config", aConfig) // only A has it

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	time.Sleep(syncSettleWindow)

	if got := b.ReadSave("settings.ini"); got != "" {
		t.Errorf("a location the peer never configured produced %q in its save folder", got)
	}
	if got := b.ReadSave("save.sav"); got != "primary data" {
		t.Errorf("primary save = %q, want it synced regardless", got)
	}
	// A keeps its own copy; nothing is lost on the side that has the folder.
	if got := readIn(aConfig, "settings.ini"); got != "fullscreen=1" {
		t.Errorf("A's own config = %q, want it untouched", got)
	}
}

// Changes flow both ways, per location, and each location's history is its
// own: editing the config folder on B must reach A without disturbing the
// save folder on either side.
func TestMultiRoot_SecondFolderSyncsBothWays(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "BothWays", map[string]string{
		"save.sav": "primary data",
	})

	aConfig := extraDir(t, a, "config")
	bConfig := extraDir(t, b, "config")
	writeIn(t, aConfig, "settings.ini", "v1")
	addRoot(t, a, gameID, "config", aConfig)
	addRoot(t, b, gameID, "config", bConfig)

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "settings.ini") == "v1"
	}) {
		t.Fatalf("first pass never delivered the config: %q", readIn(bConfig, "settings.ini"))
	}

	// Now B edits it and syncs back.
	time.Sleep(syncSettleWindow)
	writeIn(t, bConfig, "settings.ini", "v2-from-b")
	time.Sleep(syncSettleWindow)
	b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(aConfig, "settings.ini") == "v2-from-b"
	}) {
		t.Fatalf("B's edit to the second folder never reached A: %q", readIn(aConfig, "settings.ini"))
	}
	if got := a.ReadSave("save.sav"); got != "primary data" {
		t.Errorf("A's primary save changed to %q while only the config folder was edited", got)
	}
}

// Isolates the capability gate, by constructing the one situation where it is
// the only thing standing between correct behaviour and misplaced files: a
// peer that DOES list a save location but reports no proto.
//
// No shipped build does both — an older one lists nothing — which is exactly
// why the gate needs its own test. It is defence against a peer that
// advertises a location it cannot actually serve by name: ask such a peer for
// "config/settings.ini" and it resolves the path against its own save folder,
// handing back the wrong file, or deleting one. Belt and braces, but the
// braces are the ones holding up the correctness of every block request.
func TestMultiRoot_ProtoGateAloneStopsExtraLocations(t *testing.T) {
	// Lowered before anything else, and it has to be. Lowering it after the
	// locations are attached leaves a window in which both sides have the
	// location AND the peer still advertises the capability — and a sync
	// landing in that window syncs the location perfectly correctly, failing
	// this test for the one reason that is not a bug. Under -race the window
	// is wide enough to hit. Neither pairing nor tracking depends on the
	// proto, so there is nothing to lose by lowering it first.
	restore := p2p.SetServedProto(0)
	defer p2p.SetServedProto(restore)

	a, b, gameID := pairAndTrack(t, "ProtoGate", map[string]string{
		"save.sav": "primary data",
	})

	aConfig := extraDir(t, a, "config")
	bConfig := extraDir(t, b, "config")
	writeIn(t, aConfig, "settings.ini", "fullscreen=1")

	// BOTH sides have the location, so the intersection rule is satisfied and
	// the gate is the only remaining reason not to sync it.
	addRoot(t, a, gameID, "config", aConfig)
	addRoot(t, b, gameID, "config", bConfig)

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	time.Sleep(syncSettleWindow)

	if got := readIn(bConfig, "settings.ini"); got != "" {
		t.Errorf("the second location synced to a peer reporting no proto (%q); that peer cannot resolve a root name and would misplace or mis-serve the file", got)
	}
	if got := b.ReadSave("settings.ini"); got != "" {
		t.Errorf("the config file landed in the save folder as %q", got)
	}
	// The primary save is unaffected by the gate.
	if got := b.ReadSave("save.sav"); got != "primary data" {
		t.Errorf("primary save = %q, want it synced as normal", got)
	}
}
