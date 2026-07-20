package api

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/opensave/opensave/internal/snapshot"
)

// setHome points every home-derived env var at dir so portable-path
// helpers resolve against a controlled location on either OS.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", filepath.Join(dir, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "AppData", "Local"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
}

func TestPortablePathTokenizeResolve(t *testing.T) {
	homeA := t.TempDir()
	setHome(t, homeA)

	homeToken := "$HOME"
	if runtime.GOOS == "windows" {
		homeToken = "%USERPROFILE%"
	}

	p := filepath.Join(homeA, "Saved Games", "MyGame")
	tok := tokenizeSavePath(p)
	if !strings.HasPrefix(tok, homeToken+"/") {
		t.Fatalf("tokenizeSavePath(%q) = %q, want %s/ prefix", p, tok, homeToken)
	}
	if got := resolvePortablePath(tok); got != filepath.Clean(p) {
		t.Errorf("resolve(tokenize(p)) = %q, want %q", got, p)
	}

	// The point of tokens: after a "user change", the same token resolves
	// under the NEW home.
	homeB := t.TempDir()
	setHome(t, homeB)
	want := filepath.Join(homeB, "Saved Games", "MyGame")
	if got := resolvePortablePath(tok); got != filepath.Clean(want) {
		t.Errorf("resolve under new home = %q, want %q", got, want)
	}

	// A foreign-OS token must refuse to resolve, never guess.
	foreign := "%USERPROFILE%/x"
	if runtime.GOOS == "windows" {
		foreign = "$HOME/x"
	}
	if got := resolvePortablePath(foreign); got != "" {
		t.Errorf("foreign token resolved to %q, want empty", got)
	}

	// Paths outside every known root pass through unchanged.
	outside := `D:\Games\Portable\Save`
	if runtime.GOOS != "windows" {
		outside = "/opt/games/portable/save"
	}
	tok = tokenizeSavePath(outside)
	if got := resolvePortablePath(tok); got != filepath.Clean(outside) {
		t.Errorf("outside-root path = %q, want %q", got, outside)
	}
}

// exportV2 drives a selected-games export and returns the archive path.
func exportV2(t *testing.T, ts *testServer, games []map[string]string) string {
	t.Helper()
	exportPath := filepath.Join(t.TempDir(), "export.sscb")
	resp, body := ts.do(t, http.MethodPost, "/api/backup/export", map[string]any{
		"targetPath": exportPath, "games": games,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export failed: %v", body)
	}
	return exportPath
}

// readSSCB decompresses an .sscb and returns entry-name → content.
func readSSCB(t *testing.T, path string) map[string][]byte {
	t.Helper()
	src, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	raw, err := io.ReadAll(brotli.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "inner.zip")
	if err := os.WriteFile(tmp, raw, 0o666); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = data
	}
	return out
}

func waitInitialSnapshot(t *testing.T, ts *testServer, gameID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		snaps, err := ts.daemon.Store.ListSnapshots(gameID, "main")
		if err == nil && len(snaps) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("initial snapshot for %s never appeared", gameID)
}

func TestBackupExportV2Manifest(t *testing.T) {
	ts := startTestServer(t)

	if err := os.WriteFile(filepath.Join(ts.saveDir, "slot1.sav"), []byte("tracked-content"), 0o666); err != nil {
		t.Fatal(err)
	}
	resp, _ := ts.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Exported Game", "savePath": ts.saveDir})
	if resp.StatusCode != http.StatusOK {
		t.Fatal("track failed")
	}
	waitInitialSnapshot(t, ts, "exported-game")

	untracked := filepath.Join(t.TempDir(), "LooseGame", "saves")
	if err := os.MkdirAll(untracked, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(untracked, "profile.dat"), []byte("untracked-content"), 0o666); err != nil {
		t.Fatal(err)
	}

	exportPath := exportV2(t, ts, []map[string]string{
		{"id": "exported-game"},
		{"id": "scan-loose-game", "name": "Loose Game", "savePath": untracked},
	})

	entries := readSSCB(t, exportPath)
	rawManifest, ok := entries[backupManifestName]
	if !ok {
		t.Fatal("manifest.json missing from export")
	}
	var m backupManifest
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != 2 || m.OS != runtime.GOOS || len(m.Games) != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	byID := map[string]backupManifestGame{}
	for _, g := range m.Games {
		byID[g.ID] = g
	}
	if g := byID["exported-game"]; !g.Tracked || g.Name != "Exported Game" || g.PortablePath == "" {
		t.Errorf("tracked manifest entry wrong: %+v", g)
	}
	if g := byID["scan-loose-game"]; g.Tracked || g.SavePath != untracked {
		t.Errorf("untracked manifest entry wrong: %+v", g)
	}
	for _, id := range []string{"exported-game", "scan-loose-game"} {
		if len(entries["saves/"+id+".zip"]) == 0 {
			t.Errorf("saves/%s.zip missing or empty", id)
		}
	}
}

func TestBackupImportV2SnapshotsModeDefault(t *testing.T) {
	exporter := startTestServer(t)
	if err := os.WriteFile(filepath.Join(exporter.saveDir, "slot1.sav"), []byte("exported-v2"), 0o666); err != nil {
		t.Fatal(err)
	}
	exporter.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Shared Game", "savePath": exporter.saveDir})
	waitInitialSnapshot(t, exporter, "shared-game")
	exportPath := exportV2(t, exporter, []map[string]string{{"id": "shared-game"}})

	importer := startTestServer(t)
	if err := os.WriteFile(filepath.Join(importer.saveDir, "slot1.sav"), []byte("local-current"), 0o666); err != nil {
		t.Fatal(err)
	}
	importer.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Shared Game", "savePath": importer.saveDir})
	waitInitialSnapshot(t, importer, "shared-game")

	// Default mode (none given) must be the non-destructive snapshot import.
	resp, body := importer.do(t, http.MethodPost, "/api/backup/restore", map[string]string{"sourcePath": exportPath})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import failed: %v", body)
	}
	if string(body["snapshots"]) != "1" || string(body["restored"]) != "0" {
		t.Fatalf("expected 1 snapshot import and 0 restores, got %v", body)
	}

	// Live save untouched.
	got, _ := os.ReadFile(filepath.Join(importer.saveDir, "slot1.sav"))
	if string(got) != "local-current" {
		t.Fatalf("snapshots mode touched the live save: %q", got)
	}

	// The imported snapshot holds the exported content.
	snaps, err := importer.daemon.Store.ListSnapshots("shared-game", "main")
	if err != nil {
		t.Fatal(err)
	}
	var importedZip string
	for _, s := range snaps {
		if s.Comment == "Imported from backup" {
			importedZip = s.ZipPath
		}
	}
	if importedZip == "" {
		t.Fatalf("no imported snapshot found in %d snapshots", len(snaps))
	}
	restoreDir := t.TempDir()
	if err := snapshot.UnzipTo(importedZip, restoreDir); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(restoreDir, "slot1.sav"))
	if string(got) != "exported-v2" {
		t.Errorf("imported snapshot content = %q, want exported-v2", got)
	}
}

func TestBackupImportV2SnapshotsModeSkipsUntracked(t *testing.T) {
	exporter := startTestServer(t)
	loose := filepath.Join(t.TempDir(), "SoloGame", "save")
	if err := os.MkdirAll(loose, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loose, "s.dat"), []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	exportPath := exportV2(t, exporter, []map[string]string{{"id": "solo-game", "name": "Solo Game", "savePath": loose}})

	importer := startTestServer(t)
	resp, body := importer.do(t, http.MethodPost, "/api/backup/restore", map[string]string{"sourcePath": exportPath, "mode": "snapshots"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import failed: %v", body)
	}
	if string(body["skipped"]) != "1" || string(body["restored"]) != "0" || string(body["snapshots"]) != "0" {
		t.Fatalf("untracked game in snapshots mode should be skipped, got %v", body)
	}
}

func TestBackupImportV2OverwriteTracked(t *testing.T) {
	exporter := startTestServer(t)
	if err := os.WriteFile(filepath.Join(exporter.saveDir, "slot1.sav"), []byte("newer-from-backup"), 0o666); err != nil {
		t.Fatal(err)
	}
	exporter.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Shared Game", "savePath": exporter.saveDir})
	waitInitialSnapshot(t, exporter, "shared-game")
	exportPath := exportV2(t, exporter, []map[string]string{{"id": "shared-game"}})

	importer := startTestServer(t)
	if err := os.WriteFile(filepath.Join(importer.saveDir, "slot1.sav"), []byte("about-to-be-replaced"), 0o666); err != nil {
		t.Fatal(err)
	}
	importer.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Shared Game", "savePath": importer.saveDir})
	waitInitialSnapshot(t, importer, "shared-game")

	resp, body := importer.do(t, http.MethodPost, "/api/backup/restore", map[string]string{"sourcePath": exportPath, "mode": "overwrite"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import failed: %v", body)
	}
	if string(body["restored"]) != "1" {
		t.Fatalf("expected 1 restore, got %v", body)
	}

	got, _ := os.ReadFile(filepath.Join(importer.saveDir, "slot1.sav"))
	if string(got) != "newer-from-backup" {
		t.Fatalf("live save = %q, want newer-from-backup", got)
	}

	// The pre-overwrite state must survive as a safety snapshot.
	snaps, err := importer.daemon.Store.ListSnapshots("shared-game", "main")
	if err != nil {
		t.Fatal(err)
	}
	foundSafety := false
	for _, s := range snaps {
		if !strings.Contains(s.Comment, "safety") && !strings.Contains(s.Comment, "Safety") {
			continue
		}
		restoreDir := t.TempDir()
		if err := snapshot.UnzipTo(s.ZipPath, restoreDir); err != nil {
			continue
		}
		if data, _ := os.ReadFile(filepath.Join(restoreDir, "slot1.sav")); string(data) == "about-to-be-replaced" {
			foundSafety = true
			break
		}
	}
	if !foundSafety {
		t.Error("no safety snapshot holding the pre-overwrite save content")
	}
}

func TestBackupImportV2OverwriteUntracked(t *testing.T) {
	ts := startTestServer(t)

	// An untracked save exported, then damaged, then restored from backup.
	loose := filepath.Join(t.TempDir(), "PortableGame", "save")
	if err := os.MkdirAll(loose, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loose, "world.dat"), []byte("good-state"), 0o666); err != nil {
		t.Fatal(err)
	}
	exportPath := exportV2(t, ts, []map[string]string{{"id": "portable-game", "name": "Portable Game", "savePath": loose}})

	if err := os.WriteFile(filepath.Join(loose, "world.dat"), []byte("corrupted"), 0o666); err != nil {
		t.Fatal(err)
	}

	resp, body := ts.do(t, http.MethodPost, "/api/backup/restore", map[string]string{"sourcePath": exportPath, "mode": "overwrite"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import failed: %v", body)
	}
	if string(body["restored"]) != "1" {
		t.Fatalf("expected 1 restore, got %v", body)
	}

	got, _ := os.ReadFile(filepath.Join(loose, "world.dat"))
	if string(got) != "good-state" {
		t.Fatalf("restored content = %q, want good-state", got)
	}

	// The overwritten ("corrupted") content must exist in a safety zip.
	settings, err := ts.daemon.Store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	safetyDir := filepath.Join(settings.BackupsDir, "_import-safety")
	entries, err := os.ReadDir(safetyDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no safety zip written in %s: %v", safetyDir, err)
	}
	restoreDir := t.TempDir()
	if err := snapshot.UnzipTo(filepath.Join(safetyDir, entries[0].Name()), restoreDir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(restoreDir, "world.dat")); string(data) != "corrupted" {
		t.Errorf("safety zip content = %q, want the pre-overwrite state", data)
	}
}

func TestBackupImportRejectsBadMode(t *testing.T) {
	ts := startTestServer(t)
	resp, _ := ts.do(t, http.MethodPost, "/api/backup/restore", map[string]string{"sourcePath": "x.sscb", "mode": "yolo"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad mode accepted: %d", resp.StatusCode)
	}
}
