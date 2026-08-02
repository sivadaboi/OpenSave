package e2e

// Coverage for the CLI commands the rest of the suite never invokes.
//
// A command with no test is a command whose first real run is a user's. These
// are mostly thin wrappers over daemon endpoints, which is exactly the shape
// that hides field-name bugs — the two already-shipped ones ("sizeBytes" vs
// "size", "path" vs "relPath") were both in wrappers like these and both
// unmarshalled cleanly into zero values without erroring.
//
// Three commands are deliberately only tested up to their validation:
//
//   - `install` writes the user's real PATH (HKCU\Environment on Windows), which
//     is outside the sandbox this harness sets up and would alter the machine
//     running the tests;
//   - `update` downloads a release and swaps the running binary;
//   - `upnp <port>` asks the actual router for a port mapping.
//
// Their argument handling is checked; their side effects are left alone.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── Backup ───────────────────────────────────────────────────────────────

// A backup nobody has restored is not a backup. This runs the round trip the
// user actually depends on: export, lose the game, import, get it back.
func TestCLI_BackupExportImportRoundTrip(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("backupgame", map[string]string{"slot1.sav": "precious"})
	c.mustRun("add", "Backup Game", dir)
	c.mustRun("snapshot", "backup-game", "before backing up")

	archive := filepath.Join(c.home, "mybackup.sscb")
	out := c.mustRun("backup", "export", archive)

	// The command appends .sscb when it is missing; here it is present, so
	// the file must exist exactly where it was asked for.
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("backup export reported success but wrote no file at %s: %v\n%s", archive, err, out)
	}
	info, _ := os.Stat(archive)
	if info.Size() == 0 {
		t.Fatalf("backup export wrote an empty archive:\n%s", out)
	}

	// Import it back over the top. This must succeed on a database that
	// already holds the game — the common case, since users restore onto a
	// machine they have been using.
	c.mustRun("backup", "import", archive, "--overwrite")

	if !strings.Contains(c.mustRun("snapshots", "backup-game"), "before backing up") {
		t.Error("the snapshot in the backup did not survive the round trip")
	}
}

// The count in the export summary is the only signal that a backup actually
// captured anything. It was read from a field the endpoint never sends
// ("count", where the API says "snapshotCount" or "exported"), so it was
// always zero and every export — full or empty — printed the same line. A
// wrong field name unmarshals to the zero value without erroring, which is
// why this needs the real binary against a real daemon to catch.
func TestCLI_BackupExportReportsWhatItCaptured(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("countgame", map[string]string{"slot1.sav": "data"})
	c.mustRun("add", "Count Game", dir)
	c.mustRun("snapshot", "count-game", "one")
	c.mustRun("snapshot", "count-game", "two")

	out := c.mustRun("backup", "export", filepath.Join(c.home, "counted.sscb"))
	if strings.Contains(out, "captured nothing") {
		t.Errorf("a backup holding snapshots reported capturing nothing:\n%s", out)
	}
	if !strings.Contains(out, "Exported") {
		t.Errorf("the export summary does not say what it exported:\n%s", out)
	}
}

func TestCLI_BackupRejectsAMissingArchive(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()
	out := c.mustFail("backup", "import", filepath.Join(c.home, "does-not-exist.sscb"))
	if !strings.Contains(strings.ToLower(out), "no backup") {
		t.Errorf("importing a missing archive should say so plainly, got:\n%s", out)
	}
}

// ── Export ───────────────────────────────────────────────────────────────

// "Give me my files back" — the one operation that must work when everything
// else about the app has gone wrong, so it must not depend on a running
// daemon, an archive format, or the GUI.
func TestCLI_ExportCopiesSavesOutVerbatim(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("exportgame", map[string]string{
		"slot1.sav":        "level one",
		"nested/slot2.sav": "level two",
	})
	c.mustRun("add", "Export Game", dir)

	dest := filepath.Join(c.home, "exported")
	out := c.mustRun("export", "export-game", dest)

	// cmdExport puts the files under <dest>/<sanitised game name>.
	root := filepath.Join(dest, "Export Game")
	if got := c.readSave(root, "slot1.sav"); got != "level one" {
		t.Errorf("exported slot1.sav = %q, want %q\n%s", got, "level one", out)
	}
	if got := c.readSave(root, "nested/slot2.sav"); got != "level two" {
		t.Errorf("exported nested/slot2.sav = %q, want %q — nested files were not copied\n%s", got, "level two", out)
	}
}

// ── Resolve ──────────────────────────────────────────────────────────────

// The resolution names are the whole interface here: a typo that exits 0
// would let a script believe it resolved a conflict it did not touch.
func TestCLI_ResolveRejectsAnUnknownResolution(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()
	dir := c.saveDir("resolvegame", map[string]string{"a.sav": "x"})
	c.mustRun("add", "Resolve Game", dir)

	out := c.mustFail("resolve", "resolve-game", "keep-whatever")
	if !strings.Contains(out, "keep-whatever") {
		t.Errorf("the rejection should name the bad resolution, got:\n%s", out)
	}
	// And it must list the ones that do work, or the user is stuck guessing.
	for _, valid := range []string{"keep-local", "keep-remote"} {
		if !strings.Contains(out, valid) {
			t.Errorf("the rejection does not mention the valid resolution %q:\n%s", valid, out)
		}
	}
}

// Resolving when nothing is in conflict must fail rather than silently
// succeed — a script looping over `conflicts` output depends on the
// difference.
func TestCLI_ResolveWithNoConflictFails(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()
	dir := c.saveDir("noconflict", map[string]string{"a.sav": "x"})
	c.mustRun("add", "No Conflict", dir)
	c.mustFail("resolve", "no-conflict", "keep-local")
}

// ── Peer records ─────────────────────────────────────────────────────────

func TestCLI_ForgetRequiresAPeerAndReportsMissingOnes(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	// No argument: usage, non-zero.
	out := c.mustFail("forget")
	if !strings.Contains(out, "unpair") {
		t.Errorf("`forget` usage should point at `unpair` for a device that still exists:\n%s", out)
	}
	// A peer that was never known.
	c.mustFail("forget", "peer-that-never-existed")
}

// ── Launch ───────────────────────────────────────────────────────────────

// Only the failure path: a successful launch starts a real game process.
func TestCLI_LaunchOnAMissingGameFails(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()
	c.mustFail("launch", "no-such-game")
	if out := c.mustFail("launch"); !strings.Contains(out, "usage") {
		t.Errorf("`launch` with no argument should print usage:\n%s", out)
	}
}

// ── Read-only status commands ────────────────────────────────────────────

// These are what a user runs when something is wrong. If any of them crashes
// or emits malformed JSON, the person is debugging the debugger.
func TestCLI_StatusCommandsAllAnswer(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("statusgame", map[string]string{"a.sav": "x"})
	c.mustRun("add", "Status Game", dir)

	cmds := [][]string{
		{"relay", "status"},
		{"cloud", "status"},
		{"links", "status-game"},
		{"version"},
	}
	// `service` manages a systemd unit; on other platforms it correctly
	// refuses and says where the equivalent setting lives, which is a
	// non-zero exit and not something to assert success on.
	if runtime.GOOS == "linux" {
		cmds = append(cmds, []string{"service", "status"})
	}

	for _, args := range cmds {
		out, code := c.run(args...)
		if code != 0 {
			t.Errorf("`opensave %s` exited %d:\n%s", strings.Join(args, " "), code, out)
			continue
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("`opensave %s` printed nothing at all", strings.Join(args, " "))
		}
	}
}

// The --json forms of the same, since scripts consume these.
func TestCLI_StatusCommandsEmitValidJSON(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("jsonstatus", map[string]string{"a.sav": "x"})
	c.mustRun("add", "Json Status", dir)

	for _, args := range [][]string{
		{"relay", "status", "--json"},
		{"cloud", "status", "--json"},
		{"links", "json-status", "--json"},
	} {
		out, code := c.run(args...)
		if code != 0 {
			t.Errorf("`opensave %s` exited %d:\n%s", strings.Join(args, " "), code, out)
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
			t.Errorf("`opensave %s` did not emit valid JSON: %v\n%s",
				strings.Join(args, " "), err, out)
		}
	}
}

// ── Shell completion ─────────────────────────────────────────────────────

// Completion scripts get `eval`'d in a user's shell rc. An empty or broken
// one breaks their terminal, not just OpenSave.
func TestCLI_CompletionEmitsAScriptPerShell(t *testing.T) {
	c := newCLI(t)
	for _, shell := range []string{"bash", "zsh"} {
		out := c.mustRun("completion", shell)
		if !strings.Contains(out, "opensave") {
			t.Errorf("`completion %s` does not mention the command it completes:\n%s", shell, out)
		}
		if len(strings.TrimSpace(out)) < 50 {
			t.Errorf("`completion %s` emitted almost nothing:\n%s", shell, out)
		}
	}
	c.mustFail("completion", "not-a-shell")
}

// ── Commands whose side effects are out of bounds ────────────────────────

// Argument handling only. Running these for real would change the machine
// (PATH, autostart), replace the binary, or ask the router for a port
// mapping — none of which belong in a test run.
func TestCLI_SideEffectingCommandsValidateTheirArguments(t *testing.T) {
	c := newCLI(t)

	// upnp needs a port; without one it must not touch the network.
	if out := c.mustFail("upnp"); !strings.Contains(out, "usage") {
		t.Errorf("`upnp` with no port should print usage:\n%s", out)
	}
	c.mustFail("upnp", "not-a-port")

	// service must reject an unknown subcommand. On platforms without a
	// systemd unit to manage it refuses earlier than that, pointing at the
	// setting that does the same job — either way it must exit non-zero and
	// explain itself rather than failing mutely.
	out := c.mustFail("service", "frobnicate")
	if strings.TrimSpace(out) == "" {
		t.Error("`service frobnicate` failed without printing anything")
	}
	if runtime.GOOS == "linux" && !strings.Contains(out, "usage") {
		t.Errorf("`service frobnicate` should print usage on Linux:\n%s", out)
	}
}

// ── Manual snapshot retention ────────────────────────────────────────────

// Manual snapshots have their own retention budget so a game that auto-saves
// constantly cannot evict them. Both the per-game limit and the global
// default must be reachable headlessly — a Steam Deck in Game Mode has no
// other way to set them.
func TestCLI_ManualSnapshotRetentionRoundTrips(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("retention", map[string]string{"a.sav": "x"})
	c.mustRun("add", "Retention Game", dir)

	// Default: kept forever, reported as 0 on the wire.
	var before struct {
		MaxManualSnapshots int `json:"maxManualSnapshots"`
	}
	decodeGame(t, c, "retention-game", &before)
	if before.MaxManualSnapshots != 0 {
		t.Errorf("a new game starts with maxManualSnapshots = %d, want 0 (keep forever)",
			before.MaxManualSnapshots)
	}

	c.mustRun("game", "retention-game", "set", "max-manual-snapshots", "7")
	var after struct {
		MaxManualSnapshots int `json:"maxManualSnapshots"`
	}
	decodeGame(t, c, "retention-game", &after)
	if after.MaxManualSnapshots != 7 {
		t.Errorf("after setting it to 7, maxManualSnapshots = %d", after.MaxManualSnapshots)
	}

	// A negative limit is meaningless and must be refused rather than stored.
	c.mustFail("game", "retention-game", "set", "max-manual-snapshots", "-1")

	// And the global default, which new games inherit.
	c.mustRun("config", "set", "manual-snapshot-limit", "4")
	if out := c.mustRun("config"); !strings.Contains(out, "manual limit") {
		t.Errorf("`config` does not report the manual snapshot limit:\n%s", out)
	}
	// 0 is the default and means "never pruned" — printing a bare 0 next to
	// the automatic limit reads as though manual snapshots are switched off.
	c.mustRun("config", "set", "manual-snapshot-limit", "0")
	if out := c.mustRun("config"); !strings.Contains(out, "keep forever") {
		t.Errorf("`config` should spell out that 0 keeps manual snapshots forever:\n%s", out)
	}
}

// decodeGame pulls one game out of `status --json` by id.
func decodeGame(t *testing.T, c *cli, gameID string, into any) {
	t.Helper()
	raw := c.mustRun("status", "--json")
	var report struct {
		Games []json.RawMessage `json:"games"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &report); err != nil {
		t.Fatalf("status --json did not decode: %v\n%s", err, raw)
	}
	for _, g := range report.Games {
		var id struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(g, &id); err != nil || id.ID != gameID {
			continue
		}
		if err := json.Unmarshal(g, into); err != nil {
			t.Fatalf("decoding game %q: %v", gameID, err)
		}
		return
	}
	t.Fatalf("game %q not present in status --json:\n%s", gameID, raw)
}
