package e2e

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/p2p"
	"github.com/opensave/opensave/testutil"
)

// Whole journeys, rather than one feature at a time.
//
// Every other test in this package proves a single behaviour in isolation.
// These chain the features together the way a person actually uses them —
// track, exclude, split across folders, play on both machines, branch,
// break something, restore, move to a new PC — because that is where the
// interactions live, and interactions are what the isolated tests cannot
// see. Each step asserts before moving on, so a failure names the step
// rather than the end state.

// waitFile waits for a path under a directory to hold exactly want.
func waitFile(t *testing.T, dir, rel, want, step string) {
	t.Helper()
	if !testutil.WaitFor(45*time.Second, func() bool { return readIn(dir, rel) == want }) {
		t.Fatalf("%s: %s/%s = %q, want %q", step, filepath.Base(dir), rel, readIn(dir, rel), want)
	}
}

func waitSave(t *testing.T, td *testutil.TestDaemon, rel, want, step string) {
	t.Helper()
	if !testutil.WaitFor(45*time.Second, func() bool { return td.ReadSave(rel) == want }) {
		t.Fatalf("%s: save/%s = %q, want %q", step, rel, td.ReadSave(rel), want)
	}
}

func syncNow(td *testutil.TestDaemon, gameID string) {
	td.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
}

// Journey 1 — the case from issue #9, start to finish.
//
// A game keeps its save and its machine-specific config in one folder.
// Two devices, an exclusion on each, ordinary play on both, then a snapshot
// and a rollback. The config must stay different on each machine forever, the
// save must stay identical, and the rollback must bring the config back —
// because rollback empties the folder first, and this is the one interaction
// where an exclusion could destroy the file it exists to protect.
func TestJourney_DeviceSpecificConfigNeverTravels(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Neva", map[string]string{
		"Progress.gs": "chapter 1",
	})

	setIgnore(t, a, gameID, "Config.gs")
	setIgnore(t, b, gameID, "Config.gs")
	time.Sleep(syncSettleWindow)

	// Each machine writes its own display config.
	a.WriteSave("Config.gs", "1440p ultrawide")
	b.WriteSave("Config.gs", "800p handheld")
	time.Sleep(syncSettleWindow)

	// Play on A.
	a.WriteSave("Progress.gs", "chapter 2")
	syncNow(a, gameID)
	waitSave(t, b, "Progress.gs", "chapter 2", "step 1: play on A")

	// Play on B.
	time.Sleep(syncSettleWindow)
	b.WriteSave("Progress.gs", "chapter 3")
	syncNow(b, gameID)
	waitSave(t, a, "Progress.gs", "chapter 3", "step 2: play on B")

	// Neither config has moved, in either direction, after two syncs.
	if got := a.ReadSave("Config.gs"); got != "1440p ultrawide" {
		t.Fatalf("step 3: A's config became %q", got)
	}
	if got := b.ReadSave("Config.gs"); got != "800p handheld" {
		t.Fatalf("step 3: B's config became %q", got)
	}

	// Snapshot on A, wreck both files, roll back.
	var snap struct {
		ID string `json:"id"`
	}
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "before the boss"}, &snap)
	if snap.ID == "" {
		t.Fatal("step 4: no snapshot id")
	}
	a.WriteSave("Progress.gs", "corrupted")
	a.WriteSave("Config.gs", "corrupted")
	a.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": snap.ID}, nil)

	if got := a.ReadSave("Progress.gs"); got != "chapter 3" {
		t.Errorf("step 5: save after rollback = %q, want chapter 3", got)
	}
	if got := a.ReadSave("Config.gs"); got != "1440p ultrawide" {
		t.Errorf("step 5: the excluded config was not restored (%q) — rollback empties the folder, so it must be in the snapshot", got)
	}

	// And the rollback must not have pushed A's config to B.
	time.Sleep(syncSettleWindow)
	syncNow(a, gameID)
	time.Sleep(syncSettleWindow)
	if got := b.ReadSave("Config.gs"); got != "800p handheld" {
		t.Errorf("step 6: B's config changed to %q after A rolled back", got)
	}
}

// Journey 2 — a game whose save is split across three folders, through its
// whole life: sync both ways, a deletion, a branch, a switch, and a rollback.
func TestJourney_SplitSaveThroughItsWholeLife(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Split", map[string]string{"save.dat": "run 1"})

	aCfg, bCfg := extraDir(t, a, "config"), extraDir(t, b, "config")
	aMods, bMods := extraDir(t, a, "mods"), extraDir(t, b, "mods")
	writeIn(t, aCfg, "settings.ini", "v1")
	writeIn(t, aMods, "hd-textures.pak", "big")
	writeIn(t, aMods, "old.pak", "remove me later")
	for _, r := range []struct{ n, ap, bp string }{{"config", aCfg, bCfg}, {"mods", aMods, bMods}} {
		addRoot(t, a, gameID, r.n, r.ap)
		addRoot(t, b, gameID, r.n, r.bp)
	}

	time.Sleep(syncSettleWindow)
	syncNow(a, gameID)
	waitFile(t, bCfg, "settings.ini", "v1", "step 1: first sync, config")
	waitFile(t, bMods, "hd-textures.pak", "big", "step 1: first sync, mods")

	// B edits a location; it must come back the other way.
	time.Sleep(syncSettleWindow)
	writeIn(t, bCfg, "settings.ini", "v2-from-B")
	syncNow(b, gameID)
	waitFile(t, aCfg, "settings.ini", "v2-from-B", "step 2: edit on B flows back")

	// A deletes a mod; the deletion must reach B and stay gone.
	time.Sleep(syncSettleWindow)
	if err := os.Remove(filepath.Join(aMods, "old.pak")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(syncSettleWindow)
	syncNow(a, gameID)
	if !testutil.WaitFor(45*time.Second, func() bool { return readIn(bMods, "old.pak") == "" }) {
		t.Fatalf("step 3: the deletion never reached B: %q", readIn(bMods, "old.pak"))
	}
	time.Sleep(syncSettleWindow)
	syncNow(b, gameID)
	time.Sleep(syncSettleWindow)
	if got := readIn(aMods, "old.pak"); got != "" {
		t.Fatalf("step 3: the deleted mod came back on A: %q", got)
	}

	// Snapshot everything, then branch off empty and switch.
	var snap struct {
		ID string `json:"id"`
	}
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "full state"}, &snap)

	a.API(http.MethodPost, "/api/games/"+gameID+"/branch",
		map[string]any{"name": "fresh", "copyCurrentSave": false}, nil)
	a.API(http.MethodPost, "/api/games/"+gameID+"/branch/switch",
		map[string]string{"name": "fresh"}, nil)

	// An empty branch means empty everywhere, not just the main folder.
	if got := a.ReadSave("save.dat"); got != "" {
		t.Errorf("step 4: main save survived the switch to an empty branch: %q", got)
	}
	if got := readIn(aCfg, "settings.ini"); got != "" {
		t.Errorf("step 4: the config folder kept the other branch's contents: %q", got)
	}
	if got := readIn(aMods, "hd-textures.pak"); got != "" {
		t.Errorf("step 4: the mods folder kept the other branch's contents: %q", got)
	}

	// Switch back: every folder returns together.
	a.API(http.MethodPost, "/api/games/"+gameID+"/branch/switch",
		map[string]string{"name": "main"}, nil)
	if got := a.ReadSave("save.dat"); got != "run 1" {
		t.Errorf("step 5: main save after switching back = %q", got)
	}
	if got := readIn(aCfg, "settings.ini"); got != "v2-from-B" {
		t.Errorf("step 5: config after switching back = %q", got)
	}
	if got := readIn(aMods, "hd-textures.pak"); got != "big" {
		t.Errorf("step 5: mods after switching back = %q", got)
	}
}

// Journey 3 — adopting both features on a library that is already running.
//
// Nobody starts here. They have games that have been syncing for weeks, and
// then add a location and an exclusion. Neither may disturb what already
// works, and both must take effect without a restart.
func TestJourney_AdoptingTheFeaturesMidLife(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "MidLife", map[string]string{"save.dat": "v1"})

	// Weeks of ordinary use, compressed.
	for i, content := range []string{"v2", "v3"} {
		time.Sleep(syncSettleWindow)
		a.WriteSave("save.dat", content)
		syncNow(a, gameID)
		waitSave(t, b, "save.dat", content, "step 1: ordinary sync round "+string(rune('A'+i)))
	}

	// Now add a location on both, and an exclusion on both.
	aCfg, bCfg := extraDir(t, a, "config"), extraDir(t, b, "config")
	writeIn(t, aCfg, "settings.ini", "adopted")
	addRoot(t, a, gameID, "config", aCfg)
	addRoot(t, b, gameID, "config", bCfg)
	setIgnore(t, a, gameID, "*.tmp")
	setIgnore(t, b, gameID, "*.tmp")

	time.Sleep(syncSettleWindow)
	syncNow(a, gameID)
	waitFile(t, bCfg, "settings.ini", "adopted", "step 2: the new location syncs")

	// The save keeps working, unchanged by any of it.
	time.Sleep(syncSettleWindow)
	a.WriteSave("save.dat", "v4")
	a.WriteSave("scratch.tmp", "should not travel")
	syncNow(a, gameID)
	waitSave(t, b, "save.dat", "v4", "step 3: the save still syncs after adoption")
	time.Sleep(syncSettleWindow)
	if got := b.ReadSave("scratch.tmp"); got != "" {
		t.Errorf("step 3: the excluded file synced anyway: %q", got)
	}

	// A change in the new location syncs on its own, with nobody pressing
	// anything — the watcher has to have picked it up without a restart.
	time.Sleep(syncSettleWindow)
	writeIn(t, aCfg, "settings.ini", "auto")
	waitFile(t, bCfg, "settings.ini", "auto", "step 4: the new location auto-syncs")
}

// Journey 4 — a fleet where one machine has not updated.
//
// The old device must be no worse off than before any of this existed: it
// gets the main save, it is never told to delete anything, and no folder
// named after a location appears inside its save.
func TestJourney_OneDeviceStillOnTheOldBuild(t *testing.T) {
	restore := p2p.SetServedProto(0)
	defer p2p.SetServedProto(restore)

	newDev := testutil.NewTestDaemon(t, "Fleet-New")
	old := testutil.NewTestDaemon(t, "Fleet-Old")
	newDev.PairWith(old)

	newDev.WriteSave("save.dat", "v1")
	newDev.WriteSave("machine.cfg", "new device")
	gameID := newDev.TrackGame("Fleet")
	old.API(http.MethodPost, "/api/games",
		map[string]string{"name": "Fleet", "savePath": old.SaveDir}, nil)

	cfg := extraDir(t, newDev, "config")
	writeIn(t, cfg, "settings.ini", "only on the new device")
	addRoot(t, newDev, gameID, "config", cfg)
	setIgnore(t, newDev, gameID, "machine.cfg")

	syncNow(newDev, gameID)
	waitSave(t, old, "save.dat", "v1", "step 1: the old device gets the main save")

	time.Sleep(syncSettleWindow)
	for _, rel := range []string{"settings.ini", "config/settings.ini"} {
		if got := old.ReadSave(rel); got != "" {
			t.Errorf("step 2: the old device received %q (%q) — it cannot place locations and must not be sent them", rel, got)
		}
	}

	// The excluded file DOES reach the old device, and that is the deliberate
	// trade rather than a gap.
	//
	// The manifest this device serves is unfiltered on purpose. Filter it and
	// a peer holding that file in its lineage reads the gap as a deletion and
	// removes its own copy — the exact harm, inflicted on the machine that
	// cannot defend itself. Between "the old device still receives a file it
	// would have received anyway" and "the old device deletes its own", the
	// first is plainly the lesser: it leaves that device exactly as it was
	// before the feature existed, and updating it fixes it.
	if got := old.ReadSave("machine.cfg"); got != "new device" {
		t.Errorf("step 2: expected the excluded file to still reach a peer that has no rule of its own, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(old.SaveDir, ".opensave-locations")); err == nil {
		t.Error("step 2: the archive's internal location folder appeared in the old device's save")
	}

	// The old device plays; its change must still come back.
	time.Sleep(syncSettleWindow)
	old.WriteSave("save.dat", "v2-from-old")
	syncNow(old, gameID)
	waitSave(t, newDev, "save.dat", "v2-from-old", "step 3: the old device can still push")

	// And the new device's own extras survived all of it.
	if got := readIn(cfg, "settings.ini"); got != "only on the new device" {
		t.Errorf("step 4: the new device's location was disturbed: %q", got)
	}
	if got := newDev.ReadSave("machine.cfg"); got != "new device" {
		t.Errorf("step 4: the new device's excluded file was disturbed: %q", got)
	}
}

// Journey 5 — the drive died.
//
// Back everything up, lose the machine, restore onto a fresh install, and
// re-pair. This is the scenario people install a save tool for, and it is the
// one that crosses every feature at once: locations, exclusions, snapshots
// and the backup format together.
func TestJourney_RestoreOntoAFreshMachineAfterALoss(t *testing.T) {
	original := testutil.NewTestDaemon(t, "Loss-Original")
	original.WriteSave("save.dat", "80 hours in")
	original.WriteSave("machine.cfg", "the old PC")
	gameID := original.TrackGame("Loss")

	cfg := extraDir(t, original, "config")
	writeIn(t, cfg, "settings.ini", "my keybinds")
	addRoot(t, original, gameID, "config", cfg)
	setIgnore(t, original, gameID, "machine.cfg")

	target := filepath.Join(testutil.TempDir(t), "before-the-crash")
	original.API(http.MethodPost, "/api/backup/export", map[string]any{
		"targetPath": target,
		"games": []map[string]string{
			{"id": gameID, "name": "Loss", "savePath": original.SaveDir},
		},
	}, nil)
	archive := target + ".sscb"
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("step 1: no archive written: %v", err)
	}

	// The replacement machine. It adds the game first — a restore targets the
	// path recorded in the archive, which on a genuinely new PC is the same
	// place, but in a test is the original daemon's own folder. Tracking it
	// here is what someone does on a new machine anyway, and it keeps the
	// restore aimed at this device.
	fresh := testutil.NewTestDaemon(t, "Loss-Fresh")
	if id := fresh.TrackGame("Loss"); id != gameID {
		t.Fatalf("step 2: both devices should derive the id %q, this one got %q", gameID, id)
	}
	if status := fresh.APIStatus(http.MethodPost, "/api/backup/restore",
		map[string]any{"sourcePath": archive, "mode": "overwrite"}, nil); status >= 400 {
		t.Fatalf("step 2: restore failed with HTTP %d: %s", status, fresh.LastError())
	}

	// The save is back, and the excluded file came with it — it was in the
	// snapshot, which is the whole reason exclusions do not touch snapshots.
	if !testutil.WaitFor(30*time.Second, func() bool { return fresh.ReadSave("save.dat") == "80 hours in" }) {
		t.Fatalf("step 3: the save did not restore: %q", fresh.ReadSave("save.dat"))
	}
	if got := fresh.ReadSave("machine.cfg"); got != "the old PC" {
		t.Errorf("step 3: the excluded file was not in the backup (%q) — an exclusion must not reduce what is backed up", got)
	}

	// The location is remembered by name and reported as needing a folder,
	// rather than its files being dropped somewhere arbitrary.
	var roots []struct {
		Name   string `json:"name"`
		Mapped bool   `json:"mapped"`
	}
	fresh.API(http.MethodGet, "/api/games/"+gameID+"/roots", nil, &roots)
	if len(roots) != 1 || roots[0].Name != "config" {
		t.Fatalf("step 4: locations after restore = %+v, want the config location recorded", roots)
	}
	if roots[0].Mapped {
		t.Error("step 4: the location claims a folder this machine has never had")
	}
	if got := fresh.ReadSave("settings.ini"); got != "" {
		t.Errorf("step 4: the unplaceable location's file was dumped in the save folder: %q", got)
	}

	// Point it at a folder here, pair with the original, and converge.
	freshCfg := extraDir(t, fresh, "config")
	addRoot(t, fresh, gameID, "config", freshCfg)
	fresh.PairWith(original)

	time.Sleep(syncSettleWindow)
	syncNow(original, gameID)
	waitFile(t, freshCfg, "settings.ini", "my keybinds", "step 5: the location syncs once it has a folder")

	// The new machine keeps its own identity file.
	time.Sleep(syncSettleWindow)
	fresh.WriteSave("machine.cfg", "the new PC")
	setIgnore(t, fresh, gameID, "machine.cfg")
	time.Sleep(syncSettleWindow)
	syncNow(fresh, gameID)
	time.Sleep(syncSettleWindow)

	if got := original.ReadSave("machine.cfg"); got != "the old PC" {
		t.Errorf("step 6: the original's identity file changed to %q", got)
	}
	if got := fresh.ReadSave("machine.cfg"); got != "the new PC" {
		t.Errorf("step 6: the new machine's identity file changed to %q", got)
	}
}

// A multi-location snapshot that went to the cloud has to come back whole.
//
// Cloud restore takes a different route from a local rollback — it downloads
// the archive, registers it as a snapshot, then restores that — so "the local
// path is covered" does not by itself say this one is.
func TestJourney_CloudRestoreBringsBackEveryLocation(t *testing.T) {
	td := testutil.NewTestDaemon(t, "CloudLoc")
	td.WriteSave("save.dat", "v1")
	gameID := td.TrackGame("CloudLoc")

	cfg := extraDir(t, td, "config")
	writeIn(t, cfg, "settings.ini", "keybinds v1")
	addRoot(t, td, gameID, "config", cfg)

	// A local folder standing in for a cloud provider: same code path as any
	// other provider, without the network.
	remote := filepath.Join(testutil.TempDir(t), "cloud")
	if err := os.MkdirAll(remote, 0o777); err != nil {
		t.Fatal(err)
	}
	td.API(http.MethodPost, "/api/settings", map[string]any{
		"cloudSync": map[string]any{"enabled": true, "provider": "local", "url": remote},
	}, nil)

	// Snapshot, push it up, then wreck both folders.
	var snap struct {
		ID string `json:"id"`
	}
	td.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]string{"comment": "before the cloud"}, &snap)
	if snap.ID == "" {
		t.Fatal("no snapshot id")
	}
	// The upload happens on its own, in the background, as every snapshot
	// does — so wait for the provider to hold THIS snapshot, by id.
	//
	// Waiting for merely any file is a trap, and one this test fell into:
	// tracking a game snapshots it immediately, and that happens before the
	// second location has been added. Both snapshots go to the cloud, and
	// whichever finishes uploading first is whichever the machine felt like
	// that run. Restoring the earlier one puts the main save back correctly —
	// so the assertion above still passes — while the location this test
	// exists to check is simply not in the archive. It looked like a flake
	// and was a test reading a snapshot it never meant to name.
	var listed []struct {
		Name       string `json:"name"`
		SnapshotID string `json:"snapshotId"`
	}
	var wanted string
	if !testutil.WaitFor(45*time.Second, func() bool {
		listed, wanted = nil, ""
		td.API(http.MethodGet, "/api/cloud/snapshots/"+gameID, nil, &listed)
		for _, l := range listed {
			if l.SnapshotID == snap.ID {
				wanted = l.Name
				return true
			}
		}
		return false
	}) {
		t.Fatalf("the snapshot never reached the cloud provider: %s", td.LastError())
	}

	// What the provider holds, for the failure messages below. This test
	// failed twice in a full-package run and passed alone, with the main save
	// restored and the second location untouched — the signature of restoring
	// an archive that predates the location. Selecting by id above rules that
	// out; if it recurs anyway, the cause is elsewhere and the next person
	// should not have to re-derive the state from scratch.
	held := fmt.Sprintf("%d cloud snapshot(s) %v, restored %s", len(listed), listed, snap.ID)

	td.WriteSave("save.dat", "ruined")
	writeIn(t, cfg, "settings.ini", "ruined")

	if status := td.APIStatus(http.MethodPost, "/api/cloud/restore/"+gameID,
		map[string]string{"fileName": wanted}, nil); status >= 400 {
		t.Fatalf("cloud restore failed with HTTP %d: %s", status, td.LastError())
	}

	if got := td.ReadSave("save.dat"); got != "v1" {
		t.Errorf("main save after cloud restore = %q, want v1 (%s)", got, held)
	}
	if got := readIn(cfg, "settings.ini"); got != "keybinds v1" {
		t.Errorf("the second location was not restored from the cloud: %q (%s)", got, held)
	}
	if got := td.ReadSave("settings.ini"); got != "" {
		t.Errorf("the location's file was restored into the save folder: %q", got)
	}
}
