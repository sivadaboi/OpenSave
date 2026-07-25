package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/daemon"
)

type testServer struct {
	base    string
	daemon  *daemon.Daemon
	server  *Server
	saveDir string
}

func startTestServer(t *testing.T) *testServer {
	t.Helper()
	home := t.TempDir()

	d, err := daemon.New(daemon.Options{HomeOverride: home})
	if err != nil {
		t.Fatalf("daemon.New error = %v", err)
	}
	// Hermetic: never let a test daemon download the (17 MB) Ludusavi
	// manifest in the background — the async fetch races t.TempDir cleanup
	// on Windows ("file in use") and wastes bandwidth on every run.
	d.Scanner.ManifestURL = ""

	srv := New(d)
	addr, err := srv.Start(0)
	if err != nil {
		t.Fatalf("server.Start error = %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		d.Stop()
	})

	saveDir := filepath.Join(t.TempDir(), "TestGameSaves")
	if err := os.MkdirAll(saveDir, 0o777); err != nil {
		t.Fatal(err)
	}

	return &testServer{base: "http://" + addr, daemon: d, server: srv, saveDir: saveDir}
}

func (ts *testServer) do(t *testing.T, method, path string, body any) (*http.Response, map[string]json.RawMessage) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, ts.base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var decoded map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

// TestCORSPreflight guards the bug where the browser's OPTIONS preflight
// for a POST/PATCH/DELETE returned 405 (no CORS headers), silently
// blocking every mutating request from the Wails webview.
func TestCORSPreflight(t *testing.T) {
	ts := startTestServer(t)

	for _, path := range []string{"/api/games", "/api/settings", "/api/peers/pair"} {
		req, err := http.NewRequest(http.MethodOptions, ts.base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "http://wails.localhost")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "content-type")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("preflight %s: %v", path, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("preflight %s status = %d, want 204", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("preflight %s Allow-Origin = %q, want *", path, got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
			t.Errorf("preflight %s missing Allow-Methods", path)
		}
	}
}

func TestStatusEndpoint(t *testing.T) {
	ts := startTestServer(t)
	resp, body := ts.do(t, http.MethodGet, "/api/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if _, ok := body["settings"]; !ok {
		t.Error("status response missing settings")
	}
	if string(body["gameCount"]) != "0" {
		t.Errorf("gameCount = %s, want 0", body["gameCount"])
	}
}

func TestSettingsRoundTripPreservesOmittedFields(t *testing.T) {
	ts := startTestServer(t)

	// Set a sync code, then update only the device name — the sync code
	// must survive (merge semantics).
	resp, _ := ts.do(t, http.MethodPost, "/api/settings", map[string]any{"syncCode": "room-42"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first settings update status = %d", resp.StatusCode)
	}
	resp, body := ts.do(t, http.MethodPost, "/api/settings", map[string]any{"deviceName": "Renamed-PC"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second settings update status = %d", resp.StatusCode)
	}
	if string(body["deviceName"]) != `"Renamed-PC"` {
		t.Errorf("deviceName = %s", body["deviceName"])
	}
	if string(body["syncCode"]) != `"room-42"` {
		t.Errorf("syncCode was lost by a partial update: %s", body["syncCode"])
	}
}

func TestGameLifecycleOverHTTP(t *testing.T) {
	ts := startTestServer(t)

	if err := os.WriteFile(filepath.Join(ts.saveDir, "slot1.sav"), []byte("progress"), 0o666); err != nil {
		t.Fatal(err)
	}

	// Track.
	resp, body := ts.do(t, http.MethodPost, "/api/games", map[string]string{
		"name": "Test Game", "savePath": ts.saveDir,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("track status = %d (%v)", resp.StatusCode, body)
	}
	if string(body["id"]) != `"test-game"` {
		t.Errorf("game id = %s, want test-game", body["id"])
	}

	// Initial snapshot should appear (save dir had content) — it runs in
	// the background now, so poll briefly.
	var game struct {
		Branches map[string]struct {
			Snapshots []struct {
				ID string `json:"id"`
			} `json:"snapshots"`
		} `json:"branches"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, body = ts.do(t, http.MethodGet, "/api/games", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatal("list games failed")
		}
		if err := json.Unmarshal(body["test-game"], &game); err != nil {
			t.Fatalf("unmarshal game: %v", err)
		}
		if len(game.Branches["main"].Snapshots) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 1 initial snapshot, got %d", len(game.Branches["main"].Snapshots))
		}
		time.Sleep(100 * time.Millisecond)
	}
	initialSnapID := game.Branches["main"].Snapshots[0].ID

	// Manual snapshot.
	resp, _ = ts.do(t, http.MethodPost, "/api/games/test-game/snapshot", map[string]string{"comment": "manual"})
	if resp.StatusCode != http.StatusOK {
		t.Fatal("manual snapshot failed")
	}

	// Corrupt the save, roll back to the initial snapshot.
	if err := os.WriteFile(filepath.Join(ts.saveDir, "slot1.sav"), []byte("corrupted"), 0o666); err != nil {
		t.Fatal(err)
	}
	resp, _ = ts.do(t, http.MethodPost, "/api/games/test-game/rollback", map[string]string{"snapshotId": initialSnapID})
	if resp.StatusCode != http.StatusOK {
		t.Fatal("rollback failed")
	}
	got, err := os.ReadFile(filepath.Join(ts.saveDir, "slot1.sav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "progress" {
		t.Errorf("rollback content = %q, want %q", got, "progress")
	}

	// Snapshot file listing (granular restore browser).
	resp2, err := http.Get(fmt.Sprintf("%s/api/games/test-game/snapshot/%s/files", ts.base, initialSnapID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var files []struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "slot1.sav" {
		t.Errorf("snapshot file listing wrong: %+v", files)
	}

	// Branch create + switch.
	resp, _ = ts.do(t, http.MethodPost, "/api/games/test-game/branch", map[string]string{"name": "Experiment"})
	if resp.StatusCode != http.StatusOK {
		t.Fatal("branch create failed")
	}
	resp, _ = ts.do(t, http.MethodPost, "/api/games/test-game/branch/switch", map[string]string{"name": "experiment"})
	if resp.StatusCode != http.StatusOK {
		t.Fatal("branch switch failed")
	}

	// Untrack.
	resp, _ = ts.do(t, http.MethodDelete, "/api/games/test-game", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("untrack failed")
	}
	resp, body = ts.do(t, http.MethodGet, "/api/games", nil)
	if _, stillThere := body["test-game"]; stillThere {
		t.Error("game should be gone after untrack")
	}
}

func TestBackupExportImportRoundTrip(t *testing.T) {
	ts := startTestServer(t)

	if err := os.WriteFile(filepath.Join(ts.saveDir, "slot1.sav"), []byte("keep me"), 0o666); err != nil {
		t.Fatal(err)
	}
	resp, _ := ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Exported Game", "savePath": ts.saveDir})
	if resp.StatusCode != http.StatusOK {
		t.Fatal("track failed")
	}
	// The initial snapshot is async; the whole-library export below needs
	// it to exist deterministically.
	waitInitialSnapshot(t, ts, "exported-game")

	exportPath := filepath.Join(t.TempDir(), "backup.sscb")
	resp, body := ts.do(t, http.MethodPost, "/api/backup/export", map[string]string{"targetPath": exportPath})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export failed: %v", body)
	}
	if string(body["snapshotCount"]) != "1" {
		t.Errorf("snapshotCount = %s, want 1", body["snapshotCount"])
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("export file missing: %v", err)
	}

	// Wipe the snapshot metadata + zips, then import the backup.
	game, err := ts.daemon.Store.GetGame("exported-game")
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := ts.daemon.Store.ListSnapshots(game.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, snap := range snaps {
		if err := ts.daemon.Store.DeleteSnapshot(snap.ID); err != nil {
			t.Fatal(err)
		}
		os.Remove(snap.ZipPath)
	}

	resp, body = ts.do(t, http.MethodPost, "/api/backup/restore", map[string]string{"sourcePath": exportPath})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import failed: %v", body)
	}
	if string(body["imported"]) != "1" {
		t.Errorf("imported = %s, want 1", body["imported"])
	}

	restored, err := ts.daemon.Store.ListSnapshots(game.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 {
		t.Fatalf("expected 1 restored snapshot, got %d", len(restored))
	}
	if _, err := os.Stat(restored[0].ZipPath); err != nil {
		t.Errorf("restored snapshot zip missing: %v", err)
	}
}

// TestDeckyPluginContract exercises the exact three endpoints the Steam
// Deck (Decky Loader) plugin calls — its wire contract must never break.
func TestDeckyPluginContract(t *testing.T) {
	ts := startTestServer(t)

	// GET /api/status
	resp, body := ts.do(t, http.MethodGet, "/api/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/status = %d", resp.StatusCode)
	}
	for _, key := range []string{"settings", "gameCount", "peerCount"} {
		if _, ok := body[key]; !ok {
			t.Errorf("/api/status missing %q", key)
		}
	}

	// GET /api/games — must be a map keyed by game id.
	if err := os.WriteFile(filepath.Join(ts.saveDir, "s.sav"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Deck Game", "savePath": ts.saveDir})
	resp, body = ts.do(t, http.MethodGet, "/api/games", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/games = %d", resp.StatusCode)
	}
	if _, ok := body["deck-game"]; !ok {
		t.Errorf("/api/games should be keyed by game id, got keys %v", keysOf(body))
	}

	// POST /api/games/sync-all — succeeds even with no online peers.
	resp, body = ts.do(t, http.MethodPost, "/api/games/sync-all", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/games/sync-all = %d (%v)", resp.StatusCode, body)
	}
	if _, ok := body["results"]; !ok {
		t.Error("/api/games/sync-all missing results")
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPresetScanEndpoint(t *testing.T) {
	ts := startTestServer(t)
	// Hermetic: point the scanner away from the real machine.
	ts.daemon.Scanner.SteamUserdataPaths = []string{}
	ts.daemon.Scanner.ResolveAppName = nil

	resp, err := http.Get(ts.base + "/api/presets/scan")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan status = %d", resp.StatusCode)
	}
	var found []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&found); err != nil {
		t.Fatalf("scan response should be a JSON array: %v", err)
	}
}

// TestDeleteSnapshotAndBranch covers the per-snapshot and per-branch
// delete endpoints: a deleted snapshot's row and zip are gone; a deleted
// branch takes all its snapshots; active/main are protected.
func TestDeleteSnapshotAndBranch(t *testing.T) {
	ts := startTestServer(t)
	if err := os.WriteFile(filepath.Join(ts.saveDir, "s.sav"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Del Game", "savePath": ts.saveDir})
	waitInitialSnapshot(t, ts, "del-game")

	// A manual second snapshot so we have one to delete.
	ts.do(t, http.MethodPost, "/api/games/del-game/snapshot", map[string]string{"comment": "manual"})
	snaps, _ := ts.daemon.Store.ListSnapshots("del-game", "main")
	if len(snaps) < 2 {
		t.Fatalf("want >=2 snapshots, got %d", len(snaps))
	}
	victim := snaps[0]

	// Delete one snapshot.
	resp, _ := ts.do(t, http.MethodDelete, "/api/games/del-game/snapshot/"+victim.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete snapshot status = %d", resp.StatusCode)
	}
	if _, err := ts.daemon.Store.GetSnapshot(victim.ID); err == nil {
		t.Error("snapshot row still present after delete")
	}
	if _, err := os.Stat(victim.ZipPath); !os.IsNotExist(err) {
		t.Error("snapshot zip not removed")
	}

	// A side branch, then delete it.
	ts.do(t, http.MethodPost, "/api/games/del-game/branch", map[string]string{"name": "side"})
	resp, _ = ts.do(t, http.MethodDelete, "/api/games/del-game/branch/side", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete branch status = %d", resp.StatusCode)
	}
	branches, _ := ts.daemon.Store.ListBranches("del-game")
	for _, b := range branches {
		if b == "side" {
			t.Error("branch 'side' still present after delete")
		}
	}

	// main is protected.
	resp, _ = ts.do(t, http.MethodDelete, "/api/games/del-game/branch/main", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("deleting main should be rejected, got %d", resp.StatusCode)
	}
}

// TestStatusExposesConflicts pins that active conflicts are reachable over
// plain HTTP. They used to be announced only on the dashboard WebSocket, which
// left the Steam Deck's Game Mode panel unable to see — let alone resolve — a
// conflict, stranding that game until the user reached Desktop Mode.
func TestStatusExposesConflicts(t *testing.T) {
	ts := startTestServer(t)

	resp, body := ts.do(t, http.MethodGet, "/api/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if _, ok := body["conflicts"]; !ok {
		t.Errorf("/api/status has no conflicts field; keys = %v", keysOf(body))
	}
	if _, ok := body["conflictCount"]; !ok {
		t.Errorf("/api/status has no conflictCount field; keys = %v", keysOf(body))
	}
	// With none active it must still be an object, never null — clients
	// iterate it directly.
	var conflicts map[string]json.RawMessage
	if err := json.Unmarshal(body["conflicts"], &conflicts); err != nil {
		t.Errorf("conflicts should decode as an object, got %s", body["conflicts"])
	}
}

// TestDaemonAddrFile pins the contract out-of-process clients rely on to find
// a running daemon — chiefly the Steam Deck's Decky plugin, which has no way
// to ask the app which port it ended up on (the configured port may have been
// taken, and Game Mode never runs the desktop app at all). The file must hold
// a dialable loopback address while the server runs, and must not linger
// afterwards pointing at a dead port.
func TestDaemonAddrFile(t *testing.T) {
	ts := startTestServer(t)

	addrFile := filepath.Join(ts.daemon.Paths.HomeDir, "daemon.addr")
	raw, err := os.ReadFile(addrFile)
	if err != nil {
		t.Fatalf("daemon.addr not written: %v", err)
	}
	addr := strings.TrimSpace(string(raw))

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("daemon.addr = %q, not host:port: %v", addr, err)
	}
	if host != "127.0.0.1" {
		t.Errorf("daemon.addr host = %q, want 127.0.0.1 (0.0.0.0 isn't dialable)", host)
	}
	if port == "0" || port == "" {
		t.Errorf("daemon.addr port = %q, want the real bound port", port)
	}

	// The advertised address must actually serve the API.
	resp, err := http.Get("http://" + addr + "/api/status")
	if err != nil {
		t.Fatalf("advertised address %q not reachable: %v", addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/status on %q = %d, want 200", addr, resp.StatusCode)
	}

	// Stopping removes it, so nothing is pointed at a dead port.
	ts.server.Stop()
	if _, err := os.Stat(addrFile); !os.IsNotExist(err) {
		t.Errorf("daemon.addr should be removed on Stop, stat err = %v", err)
	}
}

// TestBulkUntrackEndpoint covers both shapes of POST /api/games/untrack-bulk:
// untracking an explicit id list, and the {"all": true} full reset. This is
// the "remove all the saves and re-add them" escape hatch from the Reddit
// report, so it must untrack cleanly and leave nothing tracked afterward.
func TestBulkUntrackEndpoint(t *testing.T) {
	ts := startTestServer(t)

	var ids []string
	for _, name := range []string{"Alpha Game", "Beta Game", "Gamma Game"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		resp, body := ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": name, "savePath": dir})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("track %q status = %d (%v)", name, resp.StatusCode, body)
		}
		var id string
		if err := json.Unmarshal(body["id"], &id); err != nil {
			t.Fatalf("decode id: %v", err)
		}
		ids = append(ids, id)
	}

	// Untrack the first two by id.
	resp, body := ts.do(t, http.MethodPost, "/api/games/untrack-bulk", map[string]any{"ids": ids[:2]})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk untrack status = %d (%v)", resp.StatusCode, body)
	}
	var n int
	json.Unmarshal(body["untracked"], &n)
	if n != 2 {
		t.Errorf("untracked = %d, want 2", n)
	}

	resp, list := ts.do(t, http.MethodGet, "/api/games", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("list games failed")
	}
	if len(list) != 1 {
		t.Fatalf("remaining games = %d, want 1 (%v)", len(list), keysOf(list))
	}
	if _, ok := list[ids[2]]; !ok {
		t.Errorf("expected %q to remain, keys = %v", ids[2], keysOf(list))
	}

	// Full reset: untrack everything that's left.
	resp, body = ts.do(t, http.MethodPost, "/api/games/untrack-bulk", map[string]any{"all": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset status = %d (%v)", resp.StatusCode, body)
	}
	json.Unmarshal(body["untracked"], &n)
	if n != 1 {
		t.Errorf("reset untracked = %d, want 1", n)
	}
	resp, list = ts.do(t, http.MethodGet, "/api/games", nil)
	if len(list) != 0 {
		t.Errorf("after reset, games = %d, want 0 (%v)", len(list), keysOf(list))
	}
}

// TestLinkGamesEndpoint covers the manual "these two are the same game" link:
// merging one tracked game into another records an alias and removes the
// merged entry, and the alias can be listed and removed.
func TestLinkGamesEndpoint(t *testing.T) {
	ts := startTestServer(t)

	track := func(name string) string {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		resp, body := ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": name, "savePath": dir})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("track %q status = %d (%v)", name, resp.StatusCode, body)
		}
		var id string
		json.Unmarshal(body["id"], &id)
		return id
	}

	canonical := track("Steam Copy")
	alias := track("Portable Copy")

	// Merge the portable copy into the Steam copy.
	resp, body := ts.do(t, http.MethodPost, "/api/games/"+canonical+"/link", map[string]string{"alias": alias})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("link status = %d (%v)", resp.StatusCode, body)
	}

	// The merged game is gone; the canonical one remains.
	resp, list := ts.do(t, http.MethodGet, "/api/games", nil)
	if _, ok := list[alias]; ok {
		t.Errorf("aliased game %q should have been removed, keys = %v", alias, keysOf(list))
	}
	if _, ok := list[canonical]; !ok {
		t.Errorf("canonical game %q should remain, keys = %v", canonical, keysOf(list))
	}

	// The link is listed under the canonical game (array response, so fetch
	// it directly rather than through do()'s object decoder).
	aliasResp, err := http.Get(ts.base + "/api/games/" + canonical + "/aliases")
	if err != nil {
		t.Fatal(err)
	}
	var aliases []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SavePath string `json:"savePath"`
	}
	json.NewDecoder(aliasResp.Body).Decode(&aliases)
	aliasResp.Body.Close()
	if len(aliases) != 1 || aliases[0].ID != alias {
		t.Fatalf("GET aliases = %v, want one entry with id %s", aliases, alias)
	}
	// The merged game's identity comes back too, so the UI can label the link
	// with something better than a bare id.
	if aliases[0].Name == "" || aliases[0].SavePath == "" {
		t.Errorf("alias should carry the merged game's name and path, got %+v", aliases[0])
	}

	// Unlink — the merged game comes back as its own tracked entry, and the
	// link is gone.
	resp, _ = ts.do(t, http.MethodDelete, "/api/games/"+canonical+"/alias/"+alias, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unlink status = %d", resp.StatusCode)
	}
	if remaining, _ := ts.daemon.Store.ListGameAliases(canonical); len(remaining) != 0 {
		t.Errorf("aliases after unlink = %v, want empty", remaining)
	}
	resp, list = ts.do(t, http.MethodGet, "/api/games", nil)
	if _, ok := list[alias]; !ok {
		t.Errorf("unlink should restore %q to the library, keys = %v", alias, keysOf(list))
	}
	if _, ok := list[canonical]; !ok {
		t.Errorf("canonical game %q should still be present, keys = %v", canonical, keysOf(list))
	}
}

// TestTrackSecondLocation covers the fix for the UNIQUE games.id error: a
// second save location for a same-named game is tracked under a
// disambiguated id, while re-tracking the exact same folder is rejected.
func TestTrackSecondLocation(t *testing.T) {
	ts := startTestServer(t)

	dirA := filepath.Join(t.TempDir(), "balatro-a")
	dirB := filepath.Join(t.TempDir(), "balatro-b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}

	resp, a := ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Balatro", "savePath": dirA})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("track A status = %d (%v)", resp.StatusCode, a)
	}
	resp, b := ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Balatro", "savePath": dirB})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("track B (second location) status = %d (%v)", resp.StatusCode, b)
	}
	var idA, idB string
	json.Unmarshal(a["id"], &idA)
	json.Unmarshal(b["id"], &idB)
	if idA == idB || idA == "" || idB == "" {
		t.Errorf("second location should get a distinct id, got %q and %q", idA, idB)
	}

	// Re-tracking the exact same folder is a clear duplicate.
	resp, dup := ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Balatro", "savePath": dirA})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate folder track status = %d, want 409 (%v)", resp.StatusCode, dup)
	}
}
