package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// The reported failure this feature exists for: the same game tracked under
// different ids on two machines (a Steam install on one, a differently-named
// folder on the other) never synced, because each device only looked for its
// own id. With App-ID matching on, a shared Steam App ID resolves them to the
// same game.
func TestMatching_AppIDLinksDifferentlyNamedGames(t *testing.T) {
	a := testutil.NewTestDaemon(t, "AppID-A")
	b := testutil.NewTestDaemon(t, "AppID-B")
	a.PairWith(b)

	// Both devices opt in; it's off by default so a cracked and a legit copy
	// never merge by surprise.
	for _, td := range []*testutil.TestDaemon{a, b} {
		td.API(http.MethodPost, "/api/settings", map[string]any{"matchByAppId": true}, nil)
	}

	// B's folder stays empty: two pre-existing, differing save states on a
	// first sync are a genuine conflict by design, which would mask whether
	// the App-ID lookup resolved at all.
	a.WriteSave("slot1.sav", "progress from A")

	// Same title, same App ID, different local names. The App ID goes in at
	// creation: tracking kicks off a background sync, and setting it after the
	// fact races that first request, which would then auto-track instead of
	// matching.
	var gameAResp, gameBResp struct {
		ID string `json:"id"`
	}
	a.API(http.MethodPost, "/api/games",
		map[string]string{"name": "Never Grave", "savePath": a.SaveDir, "appId": "2049740"}, &gameAResp)
	b.API(http.MethodPost, "/api/games",
		map[string]string{"name": "NeverGrave Portable", "savePath": b.SaveDir, "appId": "2049740"}, &gameBResp)
	gameA := gameAResp.ID
	if gameA == "" || gameBResp.ID == "" {
		t.Fatal("tracking returned no id")
	}
	if gameA == gameBResp.ID {
		t.Fatalf("test setup is wrong: both devices produced the same game id %q", gameA)
	}
	if g, _ := a.Daemon.Store.GetGame(gameA); g.AppID != "2049740" {
		t.Fatalf("App ID did not persist through game creation (got %q)", g.AppID)
	}

	a.API(http.MethodPost, "/api/games/"+gameA+"/sync", nil, nil)

	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "progress from A"
	}) {
		t.Fatalf("App-ID matching did not resolve %q to %q — B has slot1=%q",
			gameA, gameBResp.ID, b.ReadSave("slot1.sav"))
	}

	// B must NOT have gained a duplicate game entry for A's id.
	var games map[string]gameWire
	b.API(http.MethodGet, "/api/games", nil, &games)
	if _, dup := games[gameA]; dup {
		t.Errorf("B created a second game %q instead of matching its existing one", gameA)
	}
}

// With matching off (the default), the same App ID must NOT merge the two —
// that's the protection for someone running two separate copies of a game.
func TestMatching_AppIDIgnoredWhenDisabled(t *testing.T) {
	a := testutil.NewTestDaemon(t, "NoMatch-A")
	b := testutil.NewTestDaemon(t, "NoMatch-B")
	a.PairWith(b)

	a.WriteSave("slot1.sav", "A progress")
	gameA := a.TrackGame("Some Game")
	var gameB struct {
		ID string `json:"id"`
	}
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Different Name", "savePath": b.SaveDir}, &gameB)

	a.API(http.MethodPatch, "/api/games/"+gameA, map[string]any{"appId": "999999"}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameB.ID, map[string]any{"appId": "999999"}, nil)

	a.API(http.MethodPost, "/api/games/"+gameA+"/sync", nil, nil)
	time.Sleep(8 * time.Second)

	// The two must stay distinct entries: B auto-tracks A's game under A's own
	// id rather than folding it into the game B already had. Merging them here
	// is precisely the "cracked copy ate my legit saves" failure the opt-in
	// exists to prevent.
	var games map[string]gameWire
	b.API(http.MethodGet, "/api/games", nil, &games)
	if _, ownStillThere := games[gameB.ID]; !ownStillThere {
		t.Error("B's own game disappeared — a shared App ID merged them despite matching being off")
	}
	if _, separate := games[gameA]; !separate {
		t.Errorf("expected A's game to arrive as its own entry on B, have %v", keysOf(games))
	}
}

// The manual escape hatch: link two tracked games explicitly, which works
// regardless of App IDs and is what the ambiguous-App-ID case falls back to.
func TestMatching_ManualLinkAndUnlink(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Link-A")

	a.WriteSave("slot1.sav", "canonical")
	canonical := a.TrackGame("Canonical Game")

	// A second tracked game, at its own location.
	otherDir := filepath.Join(t.TempDir(), "other-saves")
	if err := os.MkdirAll(otherDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "slot1.sav"), []byte("other"), 0o666); err != nil {
		t.Fatal(err)
	}
	var other struct {
		ID string `json:"id"`
	}
	a.API(http.MethodPost, "/api/games", map[string]string{"name": "Other Game", "savePath": otherDir}, &other)

	a.API(http.MethodPost, "/api/games/"+canonical+"/link", map[string]string{"alias": other.ID}, nil)

	var aliases []struct {
		ID string `json:"id"`
	}
	a.API(http.MethodGet, "/api/games/"+canonical+"/aliases", nil, &aliases)
	found := false
	for _, al := range aliases {
		if al.ID == other.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("after linking, %q is not listed among %q's aliases (got %+v)", other.ID, canonical, aliases)
	}

	// The merged game is removed from the library, but its files stay put.
	var games map[string]gameWire
	a.API(http.MethodGet, "/api/games", nil, &games)
	if _, still := games[other.ID]; still {
		t.Error("linked game is still listed as its own library entry")
	}
	if _, err := os.Stat(filepath.Join(otherDir, "slot1.sav")); err != nil {
		t.Errorf("linking deleted the merged game's save files: %v", err)
	}

	// Unlinking restores it.
	a.API(http.MethodDelete, "/api/games/"+canonical+"/alias/"+other.ID, nil, nil)
	a.API(http.MethodGet, "/api/games", nil, &games)
	if _, back := games[other.ID]; !back {
		t.Error("unlinking did not restore the merged game as its own entry")
	}
}

// A manual link has to survive contact with an actual transfer. Resolving the
// peer's id when handing out a manifest but not when serving blocks made
// linked games agree on what to copy and then fail to copy it — the sync
// reported "Game not found" on every block, for a game both sides could see.
func TestMatching_LinkedGameActuallyTransfersData(t *testing.T) {
	a := testutil.NewTestDaemon(t, "LinkSync-A")
	b := testutil.NewTestDaemon(t, "LinkSync-B")
	a.PairWith(b)

	a.WriteSave("slot1.sav", "A's progress")
	gameA := a.TrackGame("Elden Ring")

	var gameB struct {
		ID string `json:"id"`
	}
	b.API(http.MethodPost, "/api/games",
		map[string]string{"name": "EldenRing Repack", "savePath": b.SaveDir}, &gameB)
	if gameA == gameB.ID {
		t.Fatal("test setup is wrong: both devices produced the same game id")
	}

	// Both devices declare the pairing. Linking only on one side is not
	// enough: an id resolves in the direction it was recorded, and a sync
	// makes requests in both — the puller asks using *its own* id, so the
	// other device needs its own mapping to answer.
	b.API(http.MethodPost, "/api/games/"+gameB.ID+"/link", map[string]string{"alias": gameA}, nil)
	a.API(http.MethodPost, "/api/games/"+gameA+"/link", map[string]string{"alias": gameB.ID}, nil)

	// Sync until the peer actually takes part. A single fire-and-forget call
	// is enough at full speed and not under load: a sync only reports peers
	// it attempted, so one issued while the pairing was still settling did
	// nothing at all, and with nothing to retry it the test then waited out
	// its full minute for a transfer that was never started.
	syncToEventually(a, gameA, b.NodeID())

	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "A's progress"
	}) {
		t.Fatalf("a linked game never received the peer's save: B slot1=%q", b.ReadSave("slot1.sav"))
	}

	// And it must land in B's own game, not a duplicate under A's id.
	var games map[string]gameWire
	b.API(http.MethodGet, "/api/games", nil, &games)
	if _, dup := games[gameA]; dup {
		t.Errorf("B created a duplicate entry %q instead of using the link", gameA)
	}
}

// Backup export then import, through the real .sscb archive on disk.
func TestBackup_ExportImportRoundTrip(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Backup-A")

	a.WriteSave("slot1.sav", "backed up")
	a.WriteSave("nested/deep.sav", "also backed up")
	gameID := a.TrackGame("Backup Game")
	if !testutil.WaitFor(30*time.Second, func() bool {
		return len(snapshotsOn(a, gameID, "main")) >= 1
	}) {
		t.Fatal("no initial snapshot to export")
	}
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]string{"comment": "pre-export"}, nil)

	target := filepath.Join(t.TempDir(), "opensave-backup")
	var exportResp struct {
		Path  string `json:"path"`
		Count int    `json:"count"`
	}
	// Naming the games selects the v2 archive format (save data plus a
	// manifest). Omitting them falls back to the v1 snapshot-library dump,
	// which import treats as a different thing entirely.
	a.API(http.MethodPost, "/api/backup/export", map[string]any{
		"targetPath": target,
		"games": []map[string]string{
			{"id": gameID, "name": "Backup Game", "savePath": a.SaveDir},
		},
	}, &exportResp)

	archive := target + ".sscb"
	info, err := os.Stat(archive)
	if err != nil {
		t.Fatalf("export produced no archive at %s: %v", archive, err)
	}
	if info.Size() == 0 {
		t.Fatal("exported archive is empty")
	}

	// Import onto a second device that tracks the same title. "snapshots"
	// mode deliberately refuses to invent games it doesn't track — it adds
	// the imported state to history and leaves the live save alone until the
	// user chooses to roll back.
	b := testutil.NewTestDaemon(t, "Backup-B")
	b.WriteSave("slot1.sav", "B's own untouched save")
	if bid := b.TrackGame("Backup Game"); bid != gameID {
		t.Fatalf("expected both devices to derive the id %q, B got %q", gameID, bid)
	}
	if !testutil.WaitFor(30*time.Second, func() bool {
		return len(snapshotsOn(b, gameID, "main")) >= 1
	}) {
		t.Fatal("B never took its initial snapshot")
	}
	before := len(snapshotsOn(b, gameID, "main"))

	var restoreResp struct {
		Restored  int `json:"restored"`
		Snapshots int `json:"snapshots"`
		Skipped   int `json:"skipped"`
	}
	status := b.APIStatus(http.MethodPost, "/api/backup/restore",
		map[string]any{"sourcePath": archive, "mode": "snapshots"}, &restoreResp)
	if status >= 400 {
		t.Fatalf("importing the archive failed with HTTP %d", status)
	}
	if restoreResp.Snapshots == 0 {
		t.Fatalf("import added no snapshots (restored=%d snapshots=%d skipped=%d)",
			restoreResp.Restored, restoreResp.Snapshots, restoreResp.Skipped)
	}

	after := snapshotsOn(b, gameID, "main")
	if len(after) <= before {
		t.Errorf("snapshot count did not grow after import (%d -> %d)", before, len(after))
	}
	// The live save must be untouched in snapshots mode.
	if got := b.ReadSave("slot1.sav"); got != "B's own untouched save" {
		t.Errorf("snapshots-mode import overwrote the live save: %q", got)
	}

	// Rolling back to the imported snapshot brings A's content across.
	imported := after[len(after)-1]
	b.API(http.MethodPost, "/api/games/"+gameID+"/rollback",
		map[string]string{"snapshotId": imported.ID}, nil)
	if !testutil.WaitFor(30*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "backed up" && b.ReadSave("nested/deep.sav") == "also backed up"
	}) {
		t.Errorf("rolling back to the imported snapshot gave slot1=%q nested=%q",
			b.ReadSave("slot1.sav"), b.ReadSave("nested/deep.sav"))
	}
}

// Settings the user sets must survive a round trip through the API, including
// the list-valued ones that are easy to drop in serialisation.
func TestSettings_RoundTripIncludingLists(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Settings-A")

	excluded := filepath.Join(t.TempDir(), "goldberg")
	a.API(http.MethodPost, "/api/settings", map[string]any{
		"deviceName":   "Renamed Device",
		"matchByAppId": true,
		"excludePaths": []string{excluded},
		"speedLimit":   512, // wire name for SpeedLimitKbps
	}, nil)

	var got struct {
		DeviceName     string   `json:"deviceName"`
		MatchByAppID   bool     `json:"matchByAppId"`
		ExcludePaths   []string `json:"excludePaths"`
		SpeedLimitKbps int      `json:"speedLimit"`
	}
	a.API(http.MethodGet, "/api/settings", nil, &got)

	if got.DeviceName != "Renamed Device" {
		t.Errorf("deviceName = %q", got.DeviceName)
	}
	if !got.MatchByAppID {
		t.Error("matchByAppId did not persist")
	}
	if got.SpeedLimitKbps != 512 {
		t.Errorf("speedLimitKbps = %d, want 512", got.SpeedLimitKbps)
	}
	if len(got.ExcludePaths) != 1 {
		t.Fatalf("excludePaths = %v, want one entry", got.ExcludePaths)
	}
}

func keysOf(m map[string]gameWire) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
