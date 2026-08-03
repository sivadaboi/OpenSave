package e2e

// Coverage for three API areas that measured 0% of statements: the cloud
// backup handlers, sync-all, and peer management.
//
// Cloud is the part users trust with the copy of last resort, and it was
// reachable only through OAuth in tests — so none of it ran. It does not have
// to be: the "local" provider is a real provider that writes to a directory,
// and every handler above the transport is identical whichever one is
// configured. Pointing it at a temp folder exercises the whole path.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// useLocalCloud points the daemon's cloud backend at a directory and returns
// it, so a test can look at what actually landed there.
func useLocalCloud(t *testing.T, td *testutil.TestDaemon) string {
	t.Helper()
	dir := filepath.Join(testutil.TempDir(t), "cloud")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	td.API(http.MethodPost, "/api/settings", map[string]any{
		"cloudSync": map[string]any{
			"enabled":  true,
			"provider": "local",
			"url":      dir,
		},
	}, nil)
	return dir
}

// cloudFiles lists what is in the cloud folder right now.
func cloudFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// waitForUpload waits until the cloud folder holds a finished file.
//
// Presence is not completion: uploads create the destination and then stream
// into it, so a file that exists may still be zero bytes for a moment. A test
// that only waits for the name races the copy and fails intermittently on
// exactly the assertion it exists to make.
func waitForUpload(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	ok := testutil.WaitFor(30*time.Second, func() bool {
		names = cloudFiles(t, dir)
		if len(names) == 0 {
			return false
		}
		for _, n := range names {
			info, err := os.Stat(filepath.Join(dir, n))
			if err != nil || info.Size() == 0 {
				return false
			}
		}
		return true
	})
	if !ok {
		t.Fatalf("no finished upload appeared in the cloud folder (saw %v)", names)
	}
	return names
}

// A snapshot must reach the cloud, be listed back, and restore. This is the
// whole promise of cloud backup, and none of these three handlers had ever
// been executed by a test.
func TestCloud_SnapshotUploadsListsAndRestores(t *testing.T) {
	a := testutil.NewTestDaemon(t, "CloudRoundTrip")
	cloudDir := useLocalCloud(t, a)

	a.WriteSave("slot1.sav", "original progress")
	gameID := a.TrackGame("Cloud Game")

	// Snapshot: the daemon uploads in the background.
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot",
		map[string]any{"comment": "before the boss"}, nil)

	// A zero-byte backup is worse than none: it looks like protection that is
	// not there. waitForUpload requires a finished, non-empty file.
	waitForUpload(t, cloudDir)

	// The app must list it back for this game.
	var remote []struct {
		Name       string `json:"name"`
		SizeBytes  int64  `json:"sizeBytes"`
		Branch     string `json:"branch"`
		SnapshotID string `json:"snapshotId"`
	}
	a.API(http.MethodGet, "/api/cloud/snapshots/"+gameID, nil, &remote)
	if len(remote) == 0 {
		t.Fatalf("the cloud holds %v but the app lists no remote snapshots for the game",
			cloudFiles(t, cloudDir))
	}
	if remote[0].SizeBytes == 0 {
		t.Errorf("remote snapshot %q is listed with size 0", remote[0].Name)
	}

	// Now lose the save, and restore it from the cloud.
	a.WriteSave("slot1.sav", "ruined")
	a.API(http.MethodPost, "/api/cloud/restore/"+gameID,
		map[string]any{"fileName": remote[0].Name}, nil)

	if got := a.ReadSave("slot1.sav"); got != "original progress" {
		t.Errorf("after restoring from the cloud the save is %q, want %q", got, "original progress")
	}
}

// Restoring something that is not this game's backup must be refused. The
// file name carries the game id, and honouring a mismatched one would write
// another game's save over this one.
func TestCloud_RestoreRejectsAFileFromAnotherGame(t *testing.T) {
	a := testutil.NewTestDaemon(t, "CloudWrongGame")
	useLocalCloud(t, a)

	a.WriteSave("slot1.sav", "mine")
	gameID := a.TrackGame("Mine")

	status := a.APIStatus(http.MethodPost, "/api/cloud/restore/"+gameID,
		map[string]any{"fileName": "some-other-game__main__snap_1.zip"}, nil)
	if status < 400 {
		t.Errorf("restoring another game's backup returned %d, want a rejection", status)
	}
	if got := a.ReadSave("slot1.sav"); got != "mine" {
		t.Errorf("the save was modified by a rejected restore: %q", got)
	}
}

// Deleting a cloud backup must actually remove it, or the retention the user
// configured is a lie and the folder grows forever.
func TestCloud_DeleteRemovesTheRemoteCopy(t *testing.T) {
	a := testutil.NewTestDaemon(t, "CloudDelete")
	cloudDir := useLocalCloud(t, a)

	a.WriteSave("slot1.sav", "data")
	gameID := a.TrackGame("Delete Game")
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]any{"comment": "one"}, nil)

	name := waitForUpload(t, cloudDir)[0]

	a.API(http.MethodPost, "/api/cloud/delete/"+gameID, map[string]any{"fileName": name}, nil)

	if !testutil.WaitFor(15*time.Second, func() bool {
		for _, f := range cloudFiles(t, cloudDir) {
			if f == name {
				return false
			}
		}
		return true
	}) {
		t.Errorf("%q is still in the cloud folder after being deleted", name)
	}
}

// sync-local is the "make the cloud match what I have" repair path, and it is
// where a truncated remote copy is supposed to be re-uploaded rather than
// skipped. Deliberately corrupt the remote copy and check it gets repaired.
func TestCloud_SyncLocalRepairsATruncatedRemoteCopy(t *testing.T) {
	a := testutil.NewTestDaemon(t, "CloudRepair")
	cloudDir := useLocalCloud(t, a)

	a.WriteSave("slot1.sav", strings.Repeat("save data ", 500))
	gameID := a.TrackGame("Repair Game")
	a.API(http.MethodPost, "/api/games/"+gameID+"/snapshot", map[string]any{"comment": "full"}, nil)

	name := waitForUpload(t, cloudDir)[0]
	full := filepath.Join(cloudDir, name)
	before, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}

	// Truncate it the way an interrupted upload would have left it.
	if err := os.WriteFile(full, []byte("half a"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.API(http.MethodPost, "/api/cloud/sync-local/"+gameID, map[string]any{}, nil)

	if !testutil.WaitFor(30*time.Second, func() bool {
		info, err := os.Stat(full)
		return err == nil && info.Size() == before.Size()
	}) {
		after, _ := os.Stat(full)
		size := int64(-1)
		if after != nil {
			size = after.Size()
		}
		t.Errorf("a truncated cloud backup was not repaired: %d bytes, want %d — "+
			"a same-name check would skip it and leave the user with a broken backup",
			size, before.Size())
	}
}

// ── Sync all ─────────────────────────────────────────────────────────────

// The "Sync all" button. It had no test, and it is the control most people
// press: one game failing must not stop the rest.
func TestSyncAll_SyncsEveryTrackedGame(t *testing.T) {
	a := testutil.NewTestDaemon(t, "SyncAll-A")
	b := testutil.NewTestDaemon(t, "SyncAll-B")
	a.PairWith(b)

	// Each game needs its own folder: a daemon refuses to track two games
	// against one directory.
	names := []string{"Game One", "Game Two", "Game Three"}
	for _, name := range names {
		slug := strings.ToLower(strings.ReplaceAll(name, " ", ""))
		aDir := filepath.Join(a.SaveDir, slug)
		bDir := filepath.Join(b.SaveDir, slug)
		if err := os.MkdirAll(aDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(bDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(aDir, "save.sav"), []byte("content of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		a.API(http.MethodPost, "/api/games",
			map[string]string{"name": name, "savePath": aDir}, nil)
		b.API(http.MethodPost, "/api/games",
			map[string]string{"name": name, "savePath": bDir}, nil)
	}

	a.API(http.MethodPost, "/api/games/sync-all", map[string]any{}, nil)

	for _, name := range names {
		slug := strings.ToLower(strings.ReplaceAll(name, " ", ""))
		want := "content of " + name
		landed := filepath.Join(b.SaveDir, slug, "save.sav")
		if !testutil.WaitFor(60*time.Second, func() bool {
			raw, err := os.ReadFile(landed)
			return err == nil && string(raw) == want
		}) {
			t.Errorf("sync-all did not deliver %q to the peer (%s)", name, landed)
		}
	}
}

// ── Peers ────────────────────────────────────────────────────────────────

// Listing devices, and removing one. Both are how a user manages the pairing
// they can see in the UI, and neither had a test.
func TestPeers_ListAndRemove(t *testing.T) {
	a := testutil.NewTestDaemon(t, "PeerList-A")
	b := testutil.NewTestDaemon(t, "PeerList-B")
	a.PairWith(b)

	var listing struct {
		Peers map[string]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"peers"`
	}
	a.API(http.MethodGet, "/api/peers", nil, &listing)
	if len(listing.Peers) == 0 {
		t.Fatal("two devices are paired but the peer list is empty")
	}
	if _, ok := listing.Peers[b.NodeID()]; !ok {
		t.Fatalf("the paired device %s is missing from the peer list: %+v", b.NodeID(), listing.Peers)
	}

	// Remove it. The record must actually go, or the UI keeps offering a
	// device the user deliberately removed.
	a.API(http.MethodDelete, "/api/peers/"+b.NodeID(), nil, nil)

	var after struct {
		Peers map[string]any `json:"peers"`
	}
	a.API(http.MethodGet, "/api/peers", nil, &after)
	if _, ok := after.Peers[b.NodeID()]; ok {
		t.Errorf("the peer is still listed after being deleted: %+v", after.Peers)
	}
}
