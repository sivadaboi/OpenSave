package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── Plumbing ─────────────────────────────────────────────────────────────

// Exit codes are the CLI's contract with scripts and with `opensave service`.
// A rejected command that exits 0 turns a typo into a silent no-op.
func TestCLI_ExitCodes(t *testing.T) {
	c := newCLI(t)

	if out, code := c.run("version"); code != 0 {
		t.Errorf("version exited %d: %s", code, out)
	}
	if out, code := c.run("--help"); code != 0 {
		t.Errorf("--help exited %d: %s", code, out)
	}

	c.mustFail("definitely-not-a-command")
	c.mustFail("add")                       // required args missing
	c.mustFail("snapshot")                  // required args missing
	c.mustFail("rollback", "some-game")     // snapshot id missing
	c.mustFail("files", "some-game")        // snapshot id missing
	c.mustFail("game", "some-game", "set")  // key and value missing
}

// An unknown command must say so, not just dump usage: "unknown command" is
// what tells the reader they mistyped rather than misused.
func TestCLI_UnknownCommandExplainsItself(t *testing.T) {
	c := newCLI(t)
	out := c.mustFail("trak", "x", "y")
	if !strings.Contains(strings.ToLower(out), "unknown command") {
		t.Errorf("output does not name the problem:\n%s", out)
	}
}

// --json exists so other programs can consume this. Output that is nearly
// JSON is worse than none: it parses in testing and breaks in the field.
func TestCLI_JSONOutputIsValid(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("jsongame", map[string]string{"a.sav": "aaa"})
	c.mustRun("add", "JSON Game", dir)

	for _, args := range [][]string{
		{"status", "--json"},
		{"config", "--json"},
		{"peers", "--json"},
		{"conflicts", "--json"},
		{"snapshots", "json-game", "--json"},
		{"exclude", "list", "--json"},
		{"scanpath", "list", "--json"},
	} {
		out, code := c.run(args...)
		if code != 0 {
			t.Errorf("`%s` exited %d:\n%s", strings.Join(args, " "), code, out)
			continue
		}
		trimmed := strings.TrimSpace(out)
		if trimmed == "" {
			t.Errorf("`%s` produced no output at all", strings.Join(args, " "))
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
			t.Errorf("`%s` did not emit valid JSON: %v\n%s", strings.Join(args, " "), err, trimmed)
		}
	}
}

// Styling must never reach a pipe. There is a unit test for the helper; this
// checks the real binary, since a single fmt.Printf with a hardcoded escape
// would bypass the helper entirely.
func TestCLI_NoAnsiEscapesWhenPiped(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()
	dir := c.saveDir("ansi", map[string]string{"a.sav": "x"})
	c.mustRun("add", "Ansi Game", dir)

	for _, args := range [][]string{{"status"}, {"config"}, {"snapshots", "ansi-game"}, {"peers"}} {
		out := c.mustRun(args...)
		if strings.Contains(out, "\x1b[") {
			t.Errorf("`%s` emitted ANSI escapes into a pipe:\n%q", strings.Join(args, " "), out)
		}
	}
}

// ── Game lifecycle ───────────────────────────────────────────────────────

func TestCLI_GameLifecycle(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("lifecycle", map[string]string{"slot1.sav": "one"})

	out := c.mustRun("add", "Life Cycle", dir)
	if !strings.Contains(out, "life-cycle") {
		t.Fatalf("add did not report the game id:\n%s", out)
	}

	if out := c.mustRun("status"); !strings.Contains(out, "Life Cycle") {
		t.Errorf("status does not list the game just added:\n%s", out)
	}

	// Adding the same folder twice must be refused, not silently duplicated:
	// two entries for one folder means two sync lineages over the same files.
	c.mustFail("add", "Life Cycle Again", dir)

	// Per-game settings.
	c.mustRun("game", "life-cycle", "set", "app-id", "1245620")
	var report struct {
		Games []struct {
			ID    string `json:"id"`
			AppID string `json:"appId"`
		} `json:"games"`
	}
	c.mustJSON(&report, "status", "--json")
	var sawAppID bool
	for _, g := range report.Games {
		if g.ID == "life-cycle" && g.AppID == "1245620" {
			sawAppID = true
		}
	}
	if !sawAppID {
		t.Errorf("app-id did not persist: %+v", report.Games)
	}
	c.mustRun("game", "life-cycle", "set", "auto-sync", "false")

	// An unknown settings key must be rejected rather than quietly ignored.
	c.mustFail("game", "life-cycle", "set", "not-a-real-key", "x")

	c.mustRun("remove", "life-cycle")
	if out := c.mustRun("status"); strings.Contains(out, "Life Cycle") {
		t.Errorf("the game is still listed after remove:\n%s", out)
	}

	// Removing tracking must never touch the save on disk.
	if got := c.readSave(dir, "slot1.sav"); got != "one" {
		t.Errorf("untracking modified the save on disk: %q", got)
	}
}

func TestCLI_OperationsOnMissingThingsFail(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	c.mustFail("snapshot", "no-such-game")
	c.mustFail("snapshots", "no-such-game")
	c.mustFail("rollback", "no-such-game", "snap_1")
	c.mustFail("files", "no-such-game", "snap_1")
	c.mustFail("remove", "no-such-game")
	c.mustFail("game", "no-such-game", "set", "app-id", "1")
}

// ── History: the part that must never lose data ──────────────────────────

// The core promise. A snapshot has to survive the save being corrupted, files
// being deleted, and nested directories being involved.
func TestCLI_SnapshotAndRollbackRestoresEverything(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("history", map[string]string{
		"slot1.sav":       "original-one",
		"slot2.sav":       "original-two",
		"deep/nested.dat": "nested-original",
	})
	c.mustRun("add", "History Game", dir)
	c.mustRun("snapshot", "history-game", "-m", "known good")

	// Two: the one tracking took automatically, and the one just asked for.
	// The manual one is newest, and is the one to roll back to.
	ids := c.snapshotIDs("history-game")
	if len(ids) != 2 {
		t.Fatalf("expected the initial snapshot plus the manual one, got %v", ids)
	}
	snap := ids[0]

	// Now wreck it in three different ways at once.
	c.saveDir("history", map[string]string{"slot1.sav": "CORRUPTED"})
	if err := removeFile(dir, "slot2.sav"); err != nil {
		t.Fatal(err)
	}
	if err := removeFile(dir, "deep/nested.dat"); err != nil {
		t.Fatal(err)
	}

	c.mustRun("rollback", "history-game", snap, "--yes")

	for rel, want := range map[string]string{
		"slot1.sav":       "original-one",
		"slot2.sav":       "original-two",
		"deep/nested.dat": "nested-original",
	} {
		if got := c.readSave(dir, rel); got != want {
			t.Errorf("after rollback %s = %q, want %q", rel, got, want)
		}
	}
}

// `files` lists a snapshot's contents. It printed every entry as 0 B for a
// release because it read the wrong field name, which no error surfaced.
func TestCLI_SnapshotFilesShowsRealSizes(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("sizes", map[string]string{
		"exact15bytes.sv": "fifteen-bytes!!", // 15 bytes
		"sub/small.dat":   "seven!!",         // 7 bytes
	})
	c.mustRun("add", "Sizes Game", dir)
	c.mustRun("snapshot", "sizes-game")
	snap := c.snapshotIDs("sizes-game")[0]

	out := c.mustRun("files", "sizes-game", snap)
	if !strings.Contains(out, "exact15bytes.sv") {
		t.Fatalf("the listing omits a file that is in the snapshot:\n%s", out)
	}
	if !strings.Contains(out, "15 B") {
		t.Errorf("no real size printed for a 15-byte file — this is the "+
			"\"every save looks empty\" bug returning:\n%s", out)
	}
	if !strings.Contains(out, "7 B") {
		t.Errorf("no real size printed for the nested 7-byte file:\n%s", out)
	}
}

// Single-file restore never worked from the CLI: it sent the wrong field name
// and the endpoint rejected every request, so nothing was ever restored.
func TestCLI_SingleFileRestore(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("onefile", map[string]string{
		"keep.sav":    "keep-original",
		"restore.sav": "restore-original",
	})
	c.mustRun("add", "One File", dir)
	c.mustRun("snapshot", "one-file")
	snap := c.snapshotIDs("one-file")[0]

	c.saveDir("onefile", map[string]string{
		"keep.sav":    "keep-EDITED",
		"restore.sav": "restore-WRECKED",
	})

	c.mustRun("files", "one-file", snap, "restore.sav")

	if got := c.readSave(dir, "restore.sav"); got != "restore-original" {
		t.Errorf("restore.sav = %q, want it restored from the snapshot", got)
	}
	// Only the named file: restoring one file must not roll the whole save
	// back and quietly discard edits the user meant to keep.
	if got := c.readSave(dir, "keep.sav"); got != "keep-EDITED" {
		t.Errorf("keep.sav = %q — a single-file restore reverted an unrelated file", got)
	}
}

func TestCLI_BranchesKeepSavesApart(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("branchy", map[string]string{"slot1.sav": "main-state"})
	c.mustRun("add", "Branchy", dir)
	c.mustRun("snapshot", "branchy", "-m", "on main")

	c.mustRun("branch", "branchy", "run2")
	c.mustRun("checkout", "branchy", "run2")
	if out := c.mustRun("status"); !strings.Contains(out, "run2") {
		t.Errorf("status does not show the active branch:\n%s", out)
	}

	c.saveDir("branchy", map[string]string{"slot1.sav": "run2-state"})
	c.mustRun("snapshot", "branchy", "-m", "on run2")
	run2Count := len(c.snapshotIDs("branchy"))

	c.mustRun("checkout", "branchy", "main")
	mainCount := len(c.snapshotIDs("branchy"))

	// Each branch lists only its own history. The counts are not asserted
	// exactly: switching away from a branch takes an automatic backup of the
	// state being left behind (SwitchBranch), so main legitimately holds more
	// than the one snapshot taken by hand. What matters is that the two
	// branches have separate histories, not one shared list.
	if run2Count == 0 || mainCount == 0 {
		t.Errorf("a branch reported no snapshots: main=%d run2=%d", mainCount, run2Count)
	}

	// Switching back restores that branch's save content, which is the point
	// of branches existing.
	if got := c.readSave(dir, "slot1.sav"); got != "main-state" {
		t.Errorf("after checking out main, slot1.sav = %q, want %q", got, "main-state")
	}
	c.mustRun("checkout", "branchy", "run2")
	if got := c.readSave(dir, "slot1.sav"); got != "run2-state" {
		t.Errorf("after checking out run2, slot1.sav = %q, want %q", got, "run2-state")
	}

	// Deleting a branch must not be possible while it is the active one, or
	// the game is left pointing at history that no longer exists.
	c.mustFail("branch-delete", "branchy", "main", "--yes")
}

func TestCLI_SnapshotDeleteAndPrune(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("pruning", map[string]string{"slot1.sav": "v1"})
	c.mustRun("add", "Pruning", dir)

	for _, v := range []string{"v1", "v2", "v3"} {
		c.saveDir("pruning", map[string]string{"slot1.sav": v})
		c.mustRun("snapshot", "pruning", "-m", v)
	}
	// Four: the automatic one taken when the game was tracked, plus the three
	// asked for here.
	ids := c.snapshotIDs("pruning")
	if len(ids) != 4 {
		t.Fatalf("expected the initial snapshot plus 3 manual ones, got %v", ids)
	}

	c.mustRun("snapshot-delete", "pruning", ids[0])
	if got := c.snapshotIDs("pruning"); len(got) != 3 {
		t.Errorf("after deleting one snapshot there are %d: %v", len(got), got)
	}

	// Deleting the same snapshot twice must fail rather than report success
	// for something that is no longer there.
	c.mustFail("snapshot-delete", "pruning", ids[0])

	// Prune is bounded by the retention limit and must be safe to run when
	// there is nothing to do.
	c.mustRun("prune")
	c.mustRun("prune")
}

// untrack-all must refuse to run unattended. This is the difference between
// a mistyped command and a lost library.
//
// The scope here is deliberately what the CLI documents, not what it arguably
// ought to. `untrack-all --yes` and `cloud delete ... --yes` spell the flag
// out in their usage; `snapshot-delete` and `branch-delete` do not, and both
// run unguarded. That is consistent with itself, and adding a required flag
// to either would break any script already calling them — so this pins the
// behaviour as it stands rather than asserting a preference.
func TestCLI_UntrackAllRequiresConfirmation(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("guard", map[string]string{"slot1.sav": "keep me"})
	c.mustRun("add", "Guarded", dir)
	c.mustRun("snapshot", "guarded")

	c.mustFail("untrack-all")

	// main is protected outright, with or without confirmation: deleting it
	// would leave the game pointing at history that no longer exists.
	c.mustFail("branch-delete", "guarded", "main", "--yes")

	// Still there after all of that.
	if out := c.mustRun("status"); !strings.Contains(out, "Guarded") {
		t.Errorf("a command that should have refused removed the game:\n%s", out)
	}
	// Both survive: the one taken when the game was tracked, and the manual
	// one above.
	if ids := c.snapshotIDs("guarded"); len(ids) != 2 {
		t.Errorf("a command that should have refused deleted a snapshot: %v", ids)
	}

	// And with --yes, untrack-all clears the library but keeps the save.
	c.mustRun("untrack-all", "--yes")
	if out := c.mustRun("status"); strings.Contains(out, "Guarded") {
		t.Errorf("untrack-all --yes did not clear the library:\n%s", out)
	}
	if got := c.readSave(dir, "slot1.sav"); got != "keep me" {
		t.Errorf("untrack-all touched the save on disk: %q", got)
	}
}

// ── Settings ─────────────────────────────────────────────────────────────

func TestCLI_ConfigRoundTrip(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	c.mustRun("config", "set", "device-name", "Bench Box")
	if out := c.mustRun("config"); !strings.Contains(out, "Bench Box") {
		t.Errorf("device-name did not persist:\n%s", out)
	}

	c.mustRun("config", "set", "match-by-app-id", "true")
	out := c.mustRun("config", "--json")
	var cfg map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &cfg); err != nil {
		t.Fatalf("config --json is not valid JSON: %v\n%s", err, out)
	}
	if cfg["matchByAppId"] != true {
		t.Errorf("matchByAppId = %v, want true", cfg["matchByAppId"])
	}

	c.mustFail("config", "set", "not-a-real-setting", "x")
}

func TestCLI_ExcludeAndScanPathRoundTrip(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	// A distinctive directory name, matched against the full path. Matching a
	// bare word instead is how the first version of this test fooled itself:
	// the empty-state message reads "No excluded folders", which contains the
	// word it was searching for, so removal always looked successful.
	dir := c.saveDir("zzmarker", map[string]string{"a.sav": "x"})

	c.mustRun("exclude", "add", dir)
	if out := c.mustRun("exclude", "list"); !strings.Contains(out, dir) {
		t.Errorf("the excluded folder is not listed:\n%s", out)
	}
	c.mustRun("exclude", "remove", dir)
	if out := c.mustRun("exclude", "list"); strings.Contains(out, dir) {
		t.Errorf("the folder is still excluded after remove:\n%s", out)
	}

	c.mustRun("scanpath", "add", dir)
	if out := c.mustRun("scanpath", "list"); !strings.Contains(out, dir) {
		t.Errorf("the extra scan path is not listed:\n%s", out)
	}
	c.mustRun("scanpath", "remove", dir)
	if out := c.mustRun("scanpath", "list"); strings.Contains(out, dir) {
		t.Errorf("the scan path is still listed after remove:\n%s", out)
	}

	c.mustFail("exclude", "add")
	c.mustFail("scanpath", "add")
}

// ── Daemon lifecycle ─────────────────────────────────────────────────────

// Commands that need the daemon must say it is not running rather than
// failing with a bare connection error.
func TestCLI_WithoutDaemonSaysSo(t *testing.T) {
	c := newCLI(t)

	out, code := c.run("daemon", "status")
	if code == 0 {
		t.Errorf("daemon status reported success with no daemon:\n%s", out)
	}
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "not running") && !strings.Contains(lower, "no daemon") {
		t.Errorf("the message does not say the daemon is down:\n%s", out)
	}
}

func TestCLI_DaemonStartStatusStop(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	if out := c.mustRun("daemon", "status"); !strings.Contains(strings.ToLower(out), "running") {
		t.Errorf("daemon status does not report running:\n%s", out)
	}

	c.mustRun("daemon", "stop")
	if _, code := c.run("daemon", "status"); code == 0 {
		t.Error("daemon status still reports success after stop")
	}
	c.daemon = nil // stopped deliberately; nothing for cleanup to kill
}
