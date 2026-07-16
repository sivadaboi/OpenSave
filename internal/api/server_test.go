package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
