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

	// Snapshot on B first, and sync both sides explicitly.
	//
	// Pausing auto-sync above also stops B's watcher, so nothing refreshes its
	// record of what the last snapshot held. The sync engine reads that record
	// to decide whether a pull would overwrite work no snapshot has captured,
	// and a stale one makes it refuse — correctly, on the information it has.
	// Snapshotting by hand is what the watcher would have done. Both sides are
	// then synced explicitly, because a push only ASKS the peer to pull and a
	// peer with auto-sync off will not act on the ask.
	b.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "capture B's own edit"}, nil)
	time.Sleep(syncSettleWindow)

	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	time.Sleep(syncSettleWindow)
	b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

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

// ── Per-game exclusions ──────────────────────────────────────────────
//
// The reported case: a game keeping its save and its device-specific config
// in one folder, where syncing the config crashes the game on the other
// machine (Neva, issue #9).

func setIgnore(t *testing.T, td *testutil.TestDaemon, gameID, patterns string) {
	t.Helper()
	td.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"syncIgnore": patterns}, nil)
}

// An excluded file stays on the device that has it and never crosses.
func TestIgnore_ExcludedFileDoesNotSync(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "IgnoreBasic", map[string]string{
		"Progress.gs": "progress v1",
	})

	setIgnore(t, a, gameID, "Config.gs")
	setIgnore(t, b, gameID, "Config.gs")

	time.Sleep(syncSettleWindow)
	a.WriteSave("Config.gs", "A's machine")
	a.WriteSave("Progress.gs", "progress v2")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	// The save syncs.
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("Progress.gs") == "progress v2"
	}) {
		t.Fatalf("the save stopped syncing: %q", b.ReadSave("Progress.gs"))
	}
	time.Sleep(syncSettleWindow)

	// The excluded file does not.
	if got := b.ReadSave("Config.gs"); got != "" {
		t.Errorf("the excluded file synced to the peer as %q", got)
	}
	// And it is untouched where it lives.
	if got := a.ReadSave("Config.gs"); got != "A's machine" {
		t.Errorf("the excluded file was altered on its own device: %q", got)
	}
}

// Each device keeps its own copy, and neither overwrites the other. This is
// the point of the feature: the file is device-specific.
func TestIgnore_EachDeviceKeepsItsOwnCopy(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "IgnoreOwn", map[string]string{
		"Progress.gs": "v1",
	})
	setIgnore(t, a, gameID, "Config.gs")
	setIgnore(t, b, gameID, "Config.gs")

	time.Sleep(syncSettleWindow)
	a.WriteSave("Config.gs", "A's machine")
	b.WriteSave("Config.gs", "B's machine")
	a.WriteSave("Progress.gs", "v2")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(45*time.Second, func() bool { return b.ReadSave("Progress.gs") == "v2" }) {
		t.Fatalf("the save did not sync: %q", b.ReadSave("Progress.gs"))
	}
	time.Sleep(syncSettleWindow)
	b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	time.Sleep(syncSettleWindow)

	if got := a.ReadSave("Config.gs"); got != "A's machine" {
		t.Errorf("A's config became %q", got)
	}
	if got := b.ReadSave("Config.gs"); got != "B's machine" {
		t.Errorf("B's config became %q", got)
	}
}

// The dangerous case, and the reason the rules are applied to the lineage as
// well: a file that ALREADY synced before the rule was written.
//
// Both devices have it, so it is recorded as shared. Excluding it makes it
// vanish from one side's view — and a shared file that vanished is a deletion
// to propagate. Getting this wrong deletes the config on the other machine,
// which is worse than the crash the user was trying to avoid.
func TestIgnore_ExcludingAnAlreadySyncedFileNeverDeletesIt(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "IgnoreLater", map[string]string{
		"Progress.gs": "v1",
		"Config.gs":   "shared before the rule",
	})

	// Both devices have it, from the initial sync.
	if got := b.ReadSave("Config.gs"); got != "shared before the rule" {
		t.Fatalf("setup: B should already have the file, got %q", got)
	}

	// Only now is the rule written.
	setIgnore(t, a, gameID, "Config.gs")
	setIgnore(t, b, gameID, "Config.gs")

	// Several syncs in both directions: any of them could carry a deletion.
	for i := 0; i < 3; i++ {
		time.Sleep(syncSettleWindow)
		a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
		time.Sleep(syncSettleWindow)
		b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	}
	time.Sleep(syncSettleWindow)

	if got := b.ReadSave("Config.gs"); got != "shared before the rule" {
		t.Errorf("excluding an already-synced file DELETED it on the peer (now %q) — the rule destroyed the file it exists to protect", got)
	}
	if got := a.ReadSave("Config.gs"); got != "shared before the rule" {
		t.Errorf("the file was deleted locally too: %q", got)
	}
}

// Known rough edge, deliberately left failing-by-skip rather than quietly
// weakened: adding a rule to a game whose excluded file had ALREADY synced can
// raise one spurious conflict on the next sync, which holds the save up until
// it is answered.
//
// The merge base and the last-snapshot hash are both rewritten when the rules
// change, and neither is the cause — the prompt survives both. Nothing is
// lost when it happens: the excluded file stays put on both devices (see the
// test above), and answering the prompt clears it. But a user who adds an
// exclusion should not be asked about a divergence that does not exist, so
// this is a real defect and not a test artefact.
func TestIgnore_AddingARuleLaterShouldNotHoldUpTheSave(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "IgnoreLaterSync", map[string]string{
		"Progress.gs": "v1",
		"Config.gs":   "shared before the rule",
	})
	setIgnore(t, a, gameID, "Config.gs")
	setIgnore(t, b, gameID, "Config.gs")

	time.Sleep(syncSettleWindow)
	a.WriteSave("Progress.gs", "v2")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("Progress.gs") == "v2"
	}) {
		t.Fatalf("adding an exclusion held the save up: peer still has %q — a rule change must not read as a divergence",
			b.ReadSave("Progress.gs"))
	}
	if got := b.ReadSave("Config.gs"); got != "shared before the rule" {
		t.Errorf("the excluded file changed on the peer: %q", got)
	}
}

// Exclusions govern syncing only. Snapshots keep everything, because
// restoring empties the folder first — a file left out of snapshots would be
// destroyed by the first rollback.
func TestIgnore_ExcludedFilesAreStillSnapshottedAndRestored(t *testing.T) {
	a := testutil.NewTestDaemon(t, "IgnoreSnap")
	a.WriteSave("Progress.gs", "v1")
	a.WriteSave("Config.gs", "my machine")
	gameID := a.TrackGame("IgnoreSnap")
	setIgnore(t, a, gameID, "Config.gs")

	var snap struct {
		ID string `json:"id"`
	}
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "with config"}, &snap)
	if snap.ID == "" {
		t.Fatal("no snapshot id returned")
	}

	a.WriteSave("Progress.gs", "ruined")
	a.WriteSave("Config.gs", "ruined")
	a.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snap.ID}, nil)

	if got := a.ReadSave("Progress.gs"); got != "v1" {
		t.Errorf("save = %q, want v1", got)
	}
	if got := a.ReadSave("Config.gs"); got != "my machine" {
		t.Errorf("the excluded file was not in the snapshot (%q) — restoring would have destroyed it", got)
	}
}

// An exclusion covers every one of a game's folders, not just the main one.
//
// It did not, and nothing said so. A rule protected the save folder and was
// ignored for the extra locations, so a device-specific config living in a
// game's settings folder — which is the commonest reason to have a second
// location at all — travelled to the other machine anyway. The failure is
// silent by nature: nothing reports a file that synced.
//
// The give-away was internal. contentHashOf has always filtered every
// location, so the guard hash left the file out while the sync carried it
// across; the two disagreed about whether the file existed.
func TestIgnore_AppliesToExtraLocationsToo(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "IgnoreRoots", map[string]string{"save.dat": "v1"})

	aCfg, bCfg := extraDir(t, a, "config"), extraDir(t, b, "config")
	addRoot(t, a, gameID, "config", aCfg)
	addRoot(t, b, gameID, "config", bCfg)

	// The rule is on A ONLY, and that is what makes this test mean anything.
	//
	// With the rule on both devices it passes either way, and the first
	// version of this test did. A "push" is not an upload — it asks the peer
	// to pull — and the manifest a device serves is deliberately unfiltered,
	// so what stops an excluded file crossing is always the RECEIVING side's
	// own rule. Giving B the rule too meant B's copy of the feature did the
	// work while A's did nothing, which is precisely the bug being fixed.
	//
	// One-sided, the question is only ever "what does A do", and A is the
	// device under test. It is also the realistic shape: one machine gets the
	// rule first.
	setIgnore(t, a, gameID, "Config.gs")
	time.Sleep(syncSettleWindow)

	// B, which has no rule, writes a config into its copy of the location.
	writeIn(t, bCfg, "Config.gs", "B's machine")
	writeIn(t, bCfg, "keybinds.ini", "shared v1")
	time.Sleep(syncSettleWindow)

	syncNow(a, gameID)
	waitFile(t, aCfg, "keybinds.ini", "shared v1", "the extra location should still sync what is not excluded")
	time.Sleep(syncSettleWindow)

	if got := readIn(aCfg, "Config.gs"); got != "" {
		t.Errorf("A pulled an excluded file into its extra location: %q", got)
	}

	// A's own copy, which B has never had. A must not push it, and must not
	// delete it because B lacks it.
	writeIn(t, aCfg, "Config.gs", "A's machine")
	time.Sleep(syncSettleWindow)
	syncNow(a, gameID)
	time.Sleep(syncSettleWindow)
	if got := readIn(aCfg, "Config.gs"); got != "A's machine" {
		t.Errorf("A's excluded file was altered or removed where it lives: %q", got)
	}

	// Deleting it must not travel: filtering the lineage is what stops "we
	// both had this and now I do not" being read as a deletion to propagate,
	// and that is the path where the rule destroys the file it was written to
	// protect. B keeps its own copy.
	time.Sleep(syncSettleWindow)
	if err := os.Remove(filepath.Join(aCfg, "Config.gs")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(syncSettleWindow)
	syncNow(a, gameID)
	time.Sleep(syncSettleWindow)
	if got := readIn(bCfg, "Config.gs"); got != "B's machine" {
		t.Errorf("deleting A's excluded config deleted B's copy too: %q", got)
	}
}
