package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// Harder scenarios for games with several save folders. The tests in
// multiroot_test.go show the feature works at all; these put it through the
// situations it will actually meet.

// twoLocationPair sets up two devices sharing one game with a second save
// location mapped on both, already synced.
func twoLocationPair(t *testing.T, name string, configFiles map[string]string) (
	a, b *testutil.TestDaemon, gameID, aConfig, bConfig string) {
	t.Helper()
	a, b, gameID = pairAndTrack(t, name, map[string]string{"save.sav": "primary v1"})

	aConfig = extraDir(t, a, "config")
	bConfig = extraDir(t, b, "config")
	for rel, content := range configFiles {
		writeIn(t, aConfig, rel, content)
	}
	addRoot(t, a, gameID, "config", aConfig)
	addRoot(t, b, gameID, "config", bConfig)

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if len(configFiles) > 0 {
		ok := testutil.WaitFor(45*time.Second, func() bool {
			for rel, content := range configFiles {
				if readIn(bConfig, rel) != content {
					return false
				}
			}
			return true
		})
		if !ok {
			t.Fatalf("the second location never reached the peer during setup")
		}
	}
	return a, b, gameID, aConfig, bConfig
}

// pauseAutoSync stops both devices syncing this game on their own.
//
// Needed to stage a genuine two-sided divergence. With auto-sync running, the
// first edit reaches the other device before the second one is made, so the
// second is an ordinary later change rather than a disagreement — correct
// behaviour, and the opposite of what a conflict test needs to set up. The
// same trick is used by the deletion tests for the same reason.
func pauseAutoSync(t *testing.T, a, b *testutil.TestDaemon, gameID string) {
	t.Helper()
	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	time.Sleep(syncSettleWindow)
}

// Deleting a file in an extra location must propagate, the same as in the
// save folder. This exercises the per-location lineage: without it the peer
// reads the missing file as one it simply never had and pushes it straight
// back, so the deletion undoes itself on the next sync.
func TestMultiRootScenario_DeletionInAnExtraLocationPropagates(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocDelete", map[string]string{
		"settings.ini": "fullscreen=1",
		"keys.cfg":     "jump=space",
	})

	time.Sleep(syncSettleWindow)
	if err := os.Remove(filepath.Join(aConfig, "keys.cfg")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "keys.cfg") == ""
	}) {
		t.Fatalf("the deletion never reached the peer's second location: keys.cfg=%q", readIn(bConfig, "keys.cfg"))
	}

	// And it must stay deleted: a later sync must not resurrect it.
	time.Sleep(syncSettleWindow)
	b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	time.Sleep(syncSettleWindow)

	if got := readIn(aConfig, "keys.cfg"); got != "" {
		t.Errorf("the deleted file came back on the device that deleted it: %q", got)
	}
	if got := readIn(bConfig, "keys.cfg"); got != "" {
		t.Errorf("the deleted file came back on the peer: %q", got)
	}
	// The file that was not deleted is untouched on both sides.
	if got := readIn(bConfig, "settings.ini"); got != "fullscreen=1" {
		t.Errorf("an unrelated file in the same location changed to %q", got)
	}
}

// The headline claim of per-location lineage: a disagreement in one folder
// does not hold the others hostage.
//
// Both devices edit the config folder — a genuine two-sided divergence — while
// only A touches the save folder. The save must still reach B. Under
// whole-game granularity the contested config would block it, which is the
// behaviour this design set out to avoid.
func TestMultiRootScenario_DisagreementInOneLocationDoesNotBlockTheOther(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocIsolate", map[string]string{
		"settings.ini": "v1",
	})

	pauseAutoSync(t, a, b, gameID)
	// Both sides change the config differently: a real divergence.
	writeIn(t, aConfig, "settings.ini", "A-version")
	writeIn(t, bConfig, "settings.ini", "B-version")
	// Only A changes the save.
	a.WriteSave("save.sav", "primary v2-from-A")
	time.Sleep(syncSettleWindow)

	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("save.sav") == "primary v2-from-A"
	}) {
		t.Fatalf("the save folder was held up by a disagreement in a different folder: b=%q", b.ReadSave("save.sav"))
	}

	// The contested folder keeps each side's own version — neither is
	// overwritten without the user choosing.
	if got := readIn(aConfig, "settings.ini"); got != "A-version" {
		t.Errorf("A's contested config was changed to %q without a decision", got)
	}
	if got := readIn(bConfig, "settings.ini"); got != "B-version" {
		t.Errorf("B's contested config was overwritten with %q", got)
	}
}

// A location added to a game that is already syncing has to start working
// without anything being reset. This is how anyone will actually adopt the
// feature: on games they already track.
func TestMultiRootScenario_LocationAddedToAnAlreadySyncingGame(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "LocLater", map[string]string{"save.sav": "primary v1"})

	// Established sync first, with no extra locations at all.
	time.Sleep(syncSettleWindow)
	a.WriteSave("save.sav", "primary v2")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(45*time.Second, func() bool { return b.ReadSave("save.sav") == "primary v2" }) {
		t.Fatal("the game was not syncing before the location was added")
	}

	// Now add a second location on both, mid-life.
	aConfig := extraDir(t, a, "config")
	bConfig := extraDir(t, b, "config")
	writeIn(t, aConfig, "settings.ini", "added later")
	addRoot(t, a, gameID, "config", aConfig)
	addRoot(t, b, gameID, "config", bConfig)

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "settings.ini") == "added later"
	}) {
		t.Fatalf("a location added to an already-syncing game never synced: %q", readIn(bConfig, "settings.ini"))
	}
	if got := b.ReadSave("save.sav"); got != "primary v2" {
		t.Errorf("adding a location disturbed the save folder: %q", got)
	}
}

// Two extra locations at once, each with its own contents, going in opposite
// directions in the same sync. Nothing may cross between them.
func TestMultiRootScenario_TwoExtraLocationsStayDistinct(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "TwoLocs", map[string]string{"save.sav": "primary"})

	aConfig, bConfig := extraDir(t, a, "config"), extraDir(t, b, "config")
	aMods, bMods := extraDir(t, a, "mods"), extraDir(t, b, "mods")
	writeIn(t, aConfig, "settings.ini", "from-A-config")
	writeIn(t, aMods, "cool.pak", "from-A-mods")
	for _, r := range []struct{ name, ap, bp string }{
		{"config", aConfig, bConfig},
		{"mods", aMods, bMods},
	} {
		addRoot(t, a, gameID, r.name, r.ap)
		addRoot(t, b, gameID, r.name, r.bp)
	}

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "settings.ini") == "from-A-config" &&
			readIn(bMods, "cool.pak") == "from-A-mods"
	}) {
		t.Fatalf("two locations did not both sync: config=%q mods=%q",
			readIn(bConfig, "settings.ini"), readIn(bMods, "cool.pak"))
	}

	// Neither location's files may appear in the other, nor in the save folder.
	if got := readIn(bConfig, "cool.pak"); got != "" {
		t.Errorf("a mods file landed in the config folder: %q", got)
	}
	if got := readIn(bMods, "settings.ini"); got != "" {
		t.Errorf("a config file landed in the mods folder: %q", got)
	}
	for _, rel := range []string{"settings.ini", "cool.pak"} {
		if got := b.ReadSave(rel); got != "" {
			t.Errorf("%s landed in the save folder: %q", rel, got)
		}
	}
}

// Snapshot and roll back through the daemon's own API, with a second
// location in play. The unit test covers the manager; this covers the route
// the app actually calls, including the safety snapshot taken on the way.
func TestMultiRootScenario_RollbackThroughTheAPIRestoresEveryLocation(t *testing.T) {
	a := testutil.NewTestDaemon(t, "LocRollback")
	a.WriteSave("save.sav", "v1")
	gameID := a.TrackGame("LocRollback")

	config := extraDir(t, a, "config")
	writeIn(t, config, "settings.ini", "v1")
	addRoot(t, a, gameID, "config", config)

	var snap struct {
		ID string `json:"id"`
	}
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "both locations at v1"}, &snap)
	if snap.ID == "" {
		t.Fatal("no snapshot id returned")
	}

	// Wreck both folders.
	a.WriteSave("save.sav", "v2-ruined")
	writeIn(t, config, "settings.ini", "v2-ruined")
	writeIn(t, config, "junk.ini", "never in the snapshot")

	a.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snap.ID}, nil)

	if got := a.ReadSave("save.sav"); got != "v1" {
		t.Errorf("save = %q, want v1", got)
	}
	if got := readIn(config, "settings.ini"); got != "v1" {
		t.Errorf("config = %q, want v1 — the second location was not rolled back", got)
	}
	if got := readIn(config, "junk.ini"); got != "" {
		t.Errorf("a file added after the snapshot survived the rollback: %q", got)
	}
	if got := a.ReadSave("settings.ini"); got != "" {
		t.Errorf("the config file was rolled back into the save folder: %q", got)
	}
}

// Removing a location stops it syncing and leaves its files alone. The files
// are the point: "remove" here means "stop covering this folder", and anyone
// who reads it as "delete my mods" once will never trust the button again.
func TestMultiRootScenario_RemovingALocationLeavesItsFilesAlone(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocRemove", map[string]string{
		"settings.ini": "shared v1",
	})

	a.API(http.MethodDelete, "/api/games/"+gameID+"/roots/config", nil, nil)

	// Files on both sides survive.
	if got := readIn(aConfig, "settings.ini"); got != "shared v1" {
		t.Errorf("removing the location deleted its files: %q", got)
	}

	// A later change there no longer syncs.
	time.Sleep(syncSettleWindow)
	writeIn(t, aConfig, "settings.ini", "changed after removal")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	time.Sleep(syncSettleWindow)

	if got := readIn(bConfig, "settings.ini"); got != "shared v1" {
		t.Errorf("a removed location still synced: peer has %q", got)
	}
	// The save folder is unaffected by any of this.
	if got := b.ReadSave("save.sav"); got != "primary v1" {
		t.Errorf("removing a location disturbed the save folder: %q", got)
	}
}

// A location whose folder disappears — an unplugged drive, a deleted folder —
// must not take the save down with it, and must not read as "every file here
// was deleted" and propagate that to the peer.
func TestMultiRootScenario_MissingLocationFolderDoesNotDeleteThePeersCopy(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocGone", map[string]string{
		"settings.ini": "important",
	})

	time.Sleep(syncSettleWindow)
	// The folder vanishes on A, as if a drive were unplugged.
	if err := os.RemoveAll(aConfig); err != nil {
		t.Fatal(err)
	}
	a.WriteSave("save.sav", "primary v2")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	// The save still syncs.
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("save.sav") == "primary v2"
	}) {
		t.Fatalf("a missing extra location stopped the save syncing: %q", b.ReadSave("save.sav"))
	}

	// And the peer's copy of the vanished location is NOT deleted.
	if got := readIn(bConfig, "settings.ini"); got != "important" {
		t.Errorf("the peer's copy was deleted because the folder went missing here: %q — an unreadable location must read as absent, not empty", got)
	}
}

// The full loop for a diverged save location: both devices edit it, the
// divergence is raised rather than one side silently winning, the user
// chooses, and the choice is applied to that folder alone.
func TestMultiRootScenario_ResolveALocationConflictKeepingMine(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocResolveMine", map[string]string{
		"settings.ini": "shared v1",
	})

	pauseAutoSync(t, a, b, gameID)
	writeIn(t, aConfig, "settings.ini", "A-version")
	writeIn(t, bConfig, "settings.ini", "B-version")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	// It is raised, not resolved by whoever synced last.
	if !testutil.WaitFor(45*time.Second, func() bool {
		return len(a.Daemon.P2P.Sync.ActiveRootConflicts()) > 0
	}) {
		t.Fatal("a two-sided change in a save location raised no conflict")
	}
	got := a.Daemon.P2P.Sync.ActiveRootConflicts()[0]
	if got.Root != "config" || got.GameID != gameID {
		t.Fatalf("conflict = %+v, want the config location of %s", got, gameID)
	}
	if got.DiffTotal == 0 {
		t.Error("the conflict lists nothing as differing, so the user is asked to choose between two things it shows as identical")
	}
	// Nothing was touched while it waits.
	if readIn(aConfig, "settings.ini") != "A-version" {
		t.Error("A's copy changed before any decision was made")
	}

	a.API(http.MethodPost, "/api/games/"+gameID+"/resolve-location-conflict",
		map[string]string{"peerId": b.NodeID(), "root": "config", "resolution": "keep-local"}, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return len(a.Daemon.P2P.Sync.ActiveRootConflicts()) == 0
	}) {
		t.Fatal("the conflict never cleared after a decision")
	}
	if got := readIn(aConfig, "settings.ini"); got != "A-version" {
		t.Errorf("keeping mine changed my copy to %q", got)
	}
	// The save folder was never involved.
	if got := a.ReadSave("save.sav"); got != "primary v1" {
		t.Errorf("resolving a location changed the save folder: %q", got)
	}
}

// The other answer: adopt the peer's copy of that one folder. The safety
// snapshot taken first is what makes it undoable.
func TestMultiRootScenario_ResolveALocationConflictKeepingTheirs(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocResolveTheirs", map[string]string{
		"settings.ini": "shared v1",
	})

	pauseAutoSync(t, a, b, gameID)
	writeIn(t, aConfig, "settings.ini", "A-version")
	writeIn(t, bConfig, "settings.ini", "B-version")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return len(a.Daemon.P2P.Sync.ActiveRootConflicts()) > 0
	}) {
		t.Fatal("no conflict raised")
	}

	before := len(snapshotsOn(a, gameID, "main"))
	a.API(http.MethodPost, "/api/games/"+gameID+"/resolve-location-conflict",
		map[string]string{"peerId": b.NodeID(), "root": "config", "resolution": "keep-remote"}, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(aConfig, "settings.ini") == "B-version"
	}) {
		t.Fatalf("adopting the peer's version never applied: %q", readIn(aConfig, "settings.ini"))
	}
	if len(a.Daemon.P2P.Sync.ActiveRootConflicts()) != 0 {
		t.Error("the conflict is still open after being resolved")
	}
	// Undoable: a snapshot was taken before anything was overwritten.
	if after := len(snapshotsOn(a, gameID, "main")); after <= before {
		t.Errorf("snapshots %d -> %d; no safety snapshot was taken before overwriting", before, after)
	}
	if got := a.ReadSave("save.sav"); got != "primary v1" {
		t.Errorf("resolving a location changed the save folder: %q", got)
	}
}

// While a location waits on a decision, later syncs must not quietly answer
// it. This is the rule that makes a conflict mean anything.
func TestMultiRootScenario_AnOpenLocationConflictBlocksFurtherSyncsOfIt(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocBlocked", map[string]string{
		"settings.ini": "shared v1",
	})

	pauseAutoSync(t, a, b, gameID)
	writeIn(t, aConfig, "settings.ini", "A-version")
	writeIn(t, bConfig, "settings.ini", "B-version")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return len(a.Daemon.P2P.Sync.ActiveRootConflicts()) > 0
	}) {
		t.Fatal("no conflict raised")
	}

	// Several more syncs, from both directions.
	for i := 0; i < 3; i++ {
		time.Sleep(syncSettleWindow)
		a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
		b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	}
	time.Sleep(syncSettleWindow)

	if got := readIn(aConfig, "settings.ini"); got != "A-version" {
		t.Errorf("A's copy became %q while a decision was still pending", got)
	}
	if got := readIn(bConfig, "settings.ini"); got != "B-version" {
		t.Errorf("B's copy became %q while a decision was still pending", got)
	}
	if len(a.Daemon.P2P.Sync.ActiveRootConflicts()) == 0 {
		t.Error("the conflict disappeared without anyone answering it")
	}
}

// A change in an extra save location has to act like a change to the game:
// snapshot, then sync, without anyone pressing anything.
//
// This did not work at all when the locations feature was first built. The
// watcher took one path per game, so the main save was watched and every
// other folder was not — the feature appeared to work because the tests
// always called sync explicitly, and a real user would have found their
// settings folder silently never syncing.
func TestMultiRootScenario_ChangesInAnExtraLocationSyncOnTheirOwn(t *testing.T) {
	a, b, gameID, aConfig, bConfig := twoLocationPair(t, "LocAuto", map[string]string{
		"settings.ini": "v1",
	})
	_ = gameID

	// Control: the main save auto-syncs, so a failure below is about the
	// location rather than about auto-sync being broken generally.
	time.Sleep(syncSettleWindow)
	a.WriteSave("save.sav", "auto-primary")
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("save.sav") == "auto-primary"
	}) {
		t.Fatal("the main save did not auto-sync; this test cannot tell you anything about locations")
	}

	// The subject: no explicit sync call anywhere after this write.
	time.Sleep(syncSettleWindow)
	writeIn(t, aConfig, "settings.ini", "auto-config")

	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "settings.ini") == "auto-config"
	}) {
		t.Fatalf("a change in an extra save location never synced on its own: peer has %q — the folder is not being watched",
			readIn(bConfig, "settings.ini"))
	}
}
