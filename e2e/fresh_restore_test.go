package e2e

// Restoring a backup onto a machine that has never seen these games — the
// reason the format exists, and the one moment it has to work.
//
// It did not. `opensave backup export` with no ids sent no game list, and the
// endpoint falls back to a snapshot-library archive that records only
// snapshot files: no names, no save locations. Restoring one onto a fresh
// install matched nothing, skipped every entry, and reported success. The
// user was left with an empty app, a "restored" message, and the reason
// buried in the activity log.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The whole migration story: back up on one install, restore on a clean one,
// get the games back — tracked, with their saves.
func TestFreshRestore_BackupRestoresGamesOntoACleanInstall(t *testing.T) {
	origin := newCLI(t)
	origin.startDaemon()

	dirA := origin.saveDir("alpha", map[string]string{"hero.sav": "hero-progress"})
	dirB := origin.saveDir("beta", map[string]string{"nested/data.sav": "second-game"})
	origin.mustRun("add", "Alpha Quest", dirA)
	origin.mustRun("add", "Beta Saga", dirB)

	archive := filepath.Join(origin.home, "migrate.sscb")
	out := origin.mustRun("backup", "export", archive)
	if !strings.Contains(out, "Exported") || strings.Contains(out, "captured nothing") {
		t.Fatalf("the export did not report capturing anything:\n%s", out)
	}

	// A completely separate install: its own home, database and daemon.
	fresh := newCLI(t)
	fresh.startDaemon()
	if s := fresh.mustRun("status"); !strings.Contains(s, "Nothing tracked yet") {
		t.Fatalf("the second install was supposed to start empty:\n%s", s)
	}

	restored := fresh.mustRun("backup", "import", archive, "--overwrite")
	if strings.Contains(restored, "Nothing was restored") {
		t.Fatalf("restoring onto a clean install brought nothing back:\n%s", restored)
	}

	// Both games must be tracked, by name — not just files left on disk.
	status := fresh.mustRun("status")
	for _, name := range []string{"Alpha Quest", "Beta Saga"} {
		if !strings.Contains(status, name) {
			t.Errorf("%q is not tracked after restoring onto a clean install:\n%s", name, status)
		}
	}

	// And the saves themselves must be back, including nested files.
	if got := origin.readSave(dirA, "hero.sav"); got != "hero-progress" {
		t.Errorf("hero.sav = %q, want %q", got, "hero-progress")
	}
	if got := origin.readSave(dirB, "nested/data.sav"); got != "second-game" {
		t.Errorf("nested/data.sav = %q, want %q", got, "second-game")
	}
}

// A save folder holding exactly ONE file — which is most of them — must be
// restored as a folder, not as a loose file in the directory above it.
//
// Restoring decides between "a folder of saves" and "one save file" by
// looking at the destination, and on a machine that has never held the game
// there is nothing there to look at. Falling back to the shape of the archive
// cannot tell the two apart, and the single file was written into the PARENT
// directory: the save came back one level above where the game reads it, and
// the restore reported success. This is the shape most likely to be hit for
// real, and the least likely to be noticed.
func TestFreshRestore_ASingleFileSaveFolderIsRestoredAsAFolder(t *testing.T) {
	origin := newCLI(t)
	origin.startDaemon()

	// One file, at the root of its save folder.
	dir := origin.saveDir("solo", map[string]string{"only.sav": "the only save"})
	origin.mustRun("add", "Solo Game", dir)
	archive := filepath.Join(origin.home, "solo.sscb")
	origin.mustRun("backup", "export", archive)
	origin.stopDaemon()

	// Lose it, exactly as a reinstall would.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	fresh := newCLI(t)
	fresh.startDaemon()
	fresh.mustRun("backup", "import", archive, "--overwrite")

	// The manifest records this machine's own home, so the save comes back
	// under the restoring install's tree.
	restoredDir := filepath.Join(fresh.home, "saves", "solo")
	if got := fresh.readSave(restoredDir, "only.sav"); got != "the only save" {
		stray := filepath.Join(fresh.home, "saves", "only.sav")
		if _, err := os.Stat(stray); err == nil {
			t.Fatalf("the save was restored to %s — one directory above its folder, "+
				"where the game will never find it", stray)
		}
		t.Fatalf("only.sav did not come back at %s (got %q)", restoredDir, got)
	}

	// And it must be tracked at the folder, not at the file.
	if status := fresh.mustRun("status"); !strings.Contains(status, "Solo Game") {
		t.Errorf("the restored single-file game is not tracked:\n%s", status)
	}
}

// The default import mode only adds to games this device already tracks, so
// on a clean install it correctly skips everything. It must say so, and say
// what to do instead — reporting a bare success here is what made the
// original failure invisible.
func TestFreshRestore_SnapshotModeOnACleanInstallSaysWhatToDo(t *testing.T) {
	origin := newCLI(t)
	origin.startDaemon()
	dir := origin.saveDir("gamma", map[string]string{"save.dat": "content"})
	origin.mustRun("add", "Gamma Run", dir)
	archive := filepath.Join(origin.home, "gamma.sscb")
	origin.mustRun("backup", "export", archive)

	fresh := newCLI(t)
	fresh.startDaemon()

	// Non-zero exit: nothing was restored, and a script must be able to tell.
	out := fresh.mustFail("backup", "import", archive)
	if !strings.Contains(out, "Nothing was restored") {
		t.Errorf("a no-op restore did not say so:\n%s", out)
	}
	if !strings.Contains(out, "--overwrite") {
		t.Errorf("the message does not point at the mode that would work:\n%s", out)
	}
}

// The ordinary case: importing onto the machine the backup came from, in the
// default non-destructive mode. The saves land in snapshot history and the
// count must be reported — this reply counts them under "snapshots", and
// reading the wrong name would report a successful import as having restored
// nothing, which is exactly the failure this command was just fixed for.
func TestFreshRestore_SnapshotModeReportsWhatItImported(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("delta", map[string]string{"save.dat": "content"})
	c.mustRun("add", "Delta Run", dir)
	archive := filepath.Join(c.home, "delta.sscb")
	c.mustRun("backup", "export", archive)

	// Same install, so the game is tracked and the import has somewhere to go.
	out := c.mustRun("backup", "import", archive)
	if strings.Contains(out, "Nothing was restored") {
		t.Fatalf("importing onto the machine that made the backup reported nothing:\n%s", out)
	}
	if !strings.Contains(out, "Imported") {
		t.Errorf("the import did not report what it took in:\n%s", out)
	}

	// And the imported state must really be in the history.
	if snaps := c.mustRun("snapshots", "delta-run"); !strings.Contains(snaps, "snap_") {
		t.Errorf("no snapshot appeared after the import:\n%s", snaps)
	}
}

// Exporting with nothing tracked must fail rather than quietly writing an
// archive that restores to nothing.
func TestFreshRestore_ExportingAnEmptyInstallFails(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()
	out := c.mustFail("backup", "export", filepath.Join(c.home, "empty.sscb"))
	if !strings.Contains(strings.ToLower(out), "nothing is tracked") {
		t.Errorf("exporting an empty install should say there is nothing to back up:\n%s", out)
	}
}
