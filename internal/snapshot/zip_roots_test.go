package snapshot

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

func read(dir, rel string) string {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return string(b)
}

func entryNames(t *testing.T, zipPath string) []string {
	t.Helper()
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []string
	for _, f := range r.File {
		out = append(out, f.Name)
	}
	return out
}

// A game with one save folder must produce exactly the archive it always did.
// Snapshots taken today are restored by builds shipped years from now, and by
// builds shipped years ago via a backup file.
func TestSingleLocationArchiveIsUnchanged(t *testing.T) {
	src := t.TempDir()
	write(t, src, "save.sav", "data")
	write(t, src, "sub/other.sav", "more")

	out := filepath.Join(t.TempDir(), "snap.zip")
	if _, err := ZipRoots(src, nil, out); err != nil {
		t.Fatal(err)
	}
	for _, name := range entryNames(t, out) {
		if strings.HasPrefix(name, RootPrefix) {
			t.Errorf("a single-location archive contains %q; the prefix must appear only when there are extra locations", name)
		}
	}
	if roots, err := ArchivedRoots(out); err != nil || len(roots) != 0 {
		t.Errorf("ArchivedRoots = %v, %v; want none", roots, err)
	}
}

// The round trip everything else depends on: two folders in, two folders out,
// each file back where it came from.
func TestTwoLocationsRoundTrip(t *testing.T) {
	save := t.TempDir()
	config := t.TempDir()
	write(t, save, "player.sav", "save data")
	write(t, save, "slots/slot1.sav", "slot one")
	write(t, config, "settings.ini", "fullscreen=1")
	write(t, config, "keys/bindings.cfg", "jump=space")

	out := filepath.Join(t.TempDir(), "snap.zip")
	if _, err := ZipRoots(save, map[string]string{"config": config}, out); err != nil {
		t.Fatal(err)
	}
	if roots, _ := ArchivedRoots(out); len(roots) != 1 || roots[0] != "config" {
		t.Fatalf("ArchivedRoots = %v, want [config]", roots)
	}

	// Restore into fresh directories.
	newSave := t.TempDir()
	newConfig := t.TempDir()
	unplaced, err := UnzipRoots(out, newSave, map[string]string{"config": newConfig})
	if err != nil {
		t.Fatal(err)
	}
	if len(unplaced) != 0 {
		t.Errorf("unplaced = %v, want none", unplaced)
	}

	if got := read(newSave, "player.sav"); got != "save data" {
		t.Errorf("player.sav = %q", got)
	}
	if got := read(newSave, "slots/slot1.sav"); got != "slot one" {
		t.Errorf("slots/slot1.sav = %q", got)
	}
	if got := read(newConfig, "settings.ini"); got != "fullscreen=1" {
		t.Errorf("settings.ini = %q", got)
	}
	if got := read(newConfig, "keys/bindings.cfg"); got != "jump=space" {
		t.Errorf("bindings.cfg = %q", got)
	}

	// The critical negative: nothing from the second location may appear in
	// the save folder, under the prefix or otherwise.
	if got := read(newSave, "settings.ini"); got != "" {
		t.Errorf("a config file was restored into the save folder as %q", got)
	}
	if _, err := os.Stat(filepath.Join(newSave, ".opensave-locations")); err == nil {
		t.Error("the prefix folder was restored literally into the save folder")
	}
}

// Restoring means making a folder look as it did, so files added since must
// go — in every location, not just the primary one.
func TestRestoreClearsEveryLocation(t *testing.T) {
	save := t.TempDir()
	config := t.TempDir()
	write(t, save, "player.sav", "v1")
	write(t, config, "settings.ini", "v1")

	out := filepath.Join(t.TempDir(), "snap.zip")
	if _, err := ZipRoots(save, map[string]string{"config": config}, out); err != nil {
		t.Fatal(err)
	}

	// Both folders gain a file that was never in the snapshot.
	write(t, save, "stray-save.sav", "should not survive")
	write(t, config, "stray.ini", "should not survive")

	if _, err := UnzipRoots(out, save, map[string]string{"config": config}); err != nil {
		t.Fatal(err)
	}
	if got := read(save, "stray-save.sav"); got != "" {
		t.Errorf("a file added after the snapshot survived in the save folder: %q", got)
	}
	if got := read(config, "stray.ini"); got != "" {
		t.Errorf("a file added after the snapshot survived in the config folder: %q — locations must be cleared too", got)
	}
	if got := read(config, "settings.ini"); got != "v1" {
		t.Errorf("settings.ini = %q, want the snapshot's contents", got)
	}
}

// A location the archive holds but this device cannot place must be reported
// and skipped. Falling back to the save folder would scatter files during a
// restore — the one operation someone reaches for when things already went
// wrong.
func TestUnplaceableLocationIsReportedNotDumpedInTheSaveFolder(t *testing.T) {
	save := t.TempDir()
	config := t.TempDir()
	write(t, save, "player.sav", "save data")
	write(t, config, "settings.ini", "fullscreen=1")

	out := filepath.Join(t.TempDir(), "snap.zip")
	if _, err := ZipRoots(save, map[string]string{"config": config}, out); err != nil {
		t.Fatal(err)
	}

	newSave := t.TempDir()
	unplaced, err := UnzipRoots(out, newSave, nil) // this device has no config location
	if err != nil {
		t.Fatal(err)
	}
	if len(unplaced) != 1 || unplaced[0] != "config" {
		t.Errorf("unplaced = %v, want [config]", unplaced)
	}
	if got := read(newSave, "settings.ini"); got != "" {
		t.Errorf("the unplaceable location's file was written into the save folder as %q", got)
	}
	if got := read(newSave, "player.sav"); got != "save data" {
		t.Errorf("the primary save failed to restore (%q) because a location could not be placed", got)
	}
}

// An empty location is a state, not an absence: it has to come back empty
// rather than come back missing.
func TestEmptyLocationSurvivesTheRoundTrip(t *testing.T) {
	save := t.TempDir()
	config := t.TempDir() // deliberately empty
	write(t, save, "player.sav", "data")

	out := filepath.Join(t.TempDir(), "snap.zip")
	if _, err := ZipRoots(save, map[string]string{"config": config}, out); err != nil {
		t.Fatal(err)
	}
	if roots, _ := ArchivedRoots(out); len(roots) != 1 {
		t.Errorf("an empty location was not recorded: %v", roots)
	}

	newSave, newConfig := t.TempDir(), filepath.Join(t.TempDir(), "cfg")
	if _, err := UnzipRoots(out, newSave, map[string]string{"config": newConfig}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(newConfig); err != nil || !info.IsDir() {
		t.Errorf("the empty location did not come back as a directory: %v", err)
	}
}

// A single-file save with a second location still restores as a file, not as
// a directory named after it.
func TestSingleFilePrimaryWithAnExtraLocation(t *testing.T) {
	base := t.TempDir()
	saveFile := filepath.Join(base, "profile.sav")
	if err := os.WriteFile(saveFile, []byte("one file"), 0o666); err != nil {
		t.Fatal(err)
	}
	config := t.TempDir()
	write(t, config, "settings.ini", "fullscreen=1")

	out := filepath.Join(t.TempDir(), "snap.zip")
	if _, err := ZipRoots(saveFile, map[string]string{"config": config}, out); err != nil {
		t.Fatal(err)
	}

	newConfig := t.TempDir()
	if _, err := UnzipRoots(out, saveFile, map[string]string{"config": newConfig}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(saveFile)
	if err != nil || info.IsDir() {
		t.Fatalf("the single-file save did not restore as a file: %v", err)
	}
	if b, _ := os.ReadFile(saveFile); string(b) != "one file" {
		t.Errorf("save file = %q", b)
	}
	if got := read(newConfig, "settings.ini"); got != "fullscreen=1" {
		t.Errorf("config = %q", got)
	}
}

// An archive entry naming a location must not be able to escape that
// location's folder.
func TestEntriesCannotEscapeTheirLocation(t *testing.T) {
	save := t.TempDir()
	write(t, save, "player.sav", "data")
	out := filepath.Join(t.TempDir(), "evil.zip")

	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	hdr, _ := w.Create(RootPrefix + "config/../../escaped.txt")
	_, _ = hdr.Write([]byte("nope"))
	w.Close()
	f.Close()

	dest := t.TempDir()
	configDir := filepath.Join(dest, "config")
	if _, err := UnzipRoots(out, filepath.Join(dest, "save"), map[string]string{"config": configDir}); err == nil {
		t.Error("a traversing entry was accepted")
	}
	if _, err := os.Stat(filepath.Join(dest, "escaped.txt")); err == nil {
		t.Error("an entry escaped its location's directory")
	}
}

// The whole thing through the Manager: a snapshot of a two-location game,
// both folders wrecked, then restored.
func TestManagerSnapshotAndRestoreAcrossLocations(t *testing.T) {
	env := setup(t)
	config := t.TempDir()
	write(t, env.saveDir, "player.sav", "v1")
	write(t, config, "settings.ini", "fullscreen=1")
	if err := env.store.AddGameRoot("game1", "config", config); err != nil {
		t.Fatal(err)
	}

	snap, err := env.mgr.Create("game1", "with config", false)
	if err != nil {
		t.Fatal(err)
	}

	// Both folders change after the snapshot.
	write(t, env.saveDir, "player.sav", "v2-ruined")
	write(t, config, "settings.ini", "ruined")
	write(t, config, "junk.ini", "should not survive")

	if _, err := env.mgr.Restore("game1", snap.ID); err != nil {
		t.Fatal(err)
	}

	if got := read(env.saveDir, "player.sav"); got != "v1" {
		t.Errorf("save = %q, want the snapshot's v1", got)
	}
	if got := read(config, "settings.ini"); got != "fullscreen=1" {
		t.Errorf("config = %q, want the snapshot's contents — the second location was not restored", got)
	}
	if got := read(config, "junk.ini"); got != "" {
		t.Errorf("a file added after the snapshot survived the restore: %q", got)
	}
	if got := read(env.saveDir, "settings.ini"); got != "" {
		t.Errorf("the config file was restored into the save folder as %q", got)
	}
}

// Switching to a branch that was deliberately started empty must empty every
// one of the game's folders, not just the main save.
//
// Otherwise a "fresh run" branch inherits the previous branch's settings and
// mods — the folders are simply left as they were, so the run is not fresh
// and nothing says so.
func TestSwitchingToAnEmptyBranchClearsEveryLocation(t *testing.T) {
	env := setup(t)
	config := t.TempDir()
	write(t, env.saveDir, "player.sav", "main run")
	write(t, config, "settings.ini", "main run settings")
	if err := env.store.AddGameRoot("game1", "config", config); err != nil {
		t.Fatal(err)
	}
	// Snapshot the main branch so switching back is possible.
	if _, err := env.mgr.Create("game1", "main run", false); err != nil {
		t.Fatal(err)
	}

	// A branch deliberately started with nothing in it.
	if _, err := env.mgr.CreateBranch("game1", "fresh", false); err != nil {
		t.Fatal(err)
	}
	if err := env.mgr.SwitchBranch("game1", "fresh"); err != nil {
		t.Fatal(err)
	}

	if got := read(env.saveDir, "player.sav"); got != "" {
		t.Errorf("the main save survived the switch to an empty branch: %q", got)
	}
	if got := read(config, "settings.ini"); got != "" {
		t.Errorf("the config folder still holds the other branch's contents (%q) — an empty branch is not empty", got)
	}

	// And switching back brings everything, both folders, together.
	if err := env.mgr.SwitchBranch("game1", "main"); err != nil {
		t.Fatal(err)
	}
	if got := read(env.saveDir, "player.sav"); got != "main run" {
		t.Errorf("main save after switching back = %q", got)
	}
	if got := read(config, "settings.ini"); got != "main run settings" {
		t.Errorf("config after switching back = %q — the location did not come back with the branch", got)
	}
}
