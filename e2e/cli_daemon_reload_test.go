package e2e

// The CLI does not talk to a running daemon: every command opens a
// short-lived daemon of its own and writes the database directly. That is
// fine for reads, and quietly wrong for anything that changes which games
// exist — a game added with `opensave add` while the desktop app is running
// appeared in the list but was watched by nobody, so it got no auto-snapshots
// and no auto-sync until the app was restarted, with nothing to say so.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitForLog waits until the daemon's activity log contains a line.
func waitForLog(t *testing.T, c *cli, want string, within time.Duration) bool {
	t.Helper()
	logPath := filepath.Join(c.home, ".opensave", "opensave.log")
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(logPath); err == nil && strings.Contains(string(raw), want) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// Adding a game from the CLI must reach a daemon that is already running.
func TestCLIDaemonReload_AddingAGameReachesTheRunningDaemon(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	// The daemon started with nothing tracked, so it is watching nothing.
	if !waitForLog(t, c, "watching 0 game(s)", 20*time.Second) {
		t.Fatal("the daemon did not start with an empty watch list")
	}

	dir := c.saveDir("reload", map[string]string{"save.sav": "start"})
	c.mustRun("add", "Reload Game", dir)

	if !waitForLog(t, c, "watch list reconciled", 20*time.Second) {
		t.Fatal("the running daemon was never told about the game added from the CLI, " +
			"so it will not snapshot or sync it until restarted")
	}
	if !waitForLog(t, c, "(directory mode)", 20*time.Second) {
		t.Error("the daemon did not start watching the newly added save folder")
	}
}

// The reverse: switching auto-sync off must stop the running daemon watching
// it, or the setting appears to apply and does nothing until a restart.
func TestCLIDaemonReload_TurningAutoSyncOffStopsTheWatch(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("offswitch", map[string]string{"save.sav": "start"})
	c.mustRun("add", "Off Switch", dir)
	if !waitForLog(t, c, "watch list reconciled", 20*time.Second) {
		t.Fatal("the daemon never picked up the added game")
	}

	c.mustRun("game", "off-switch", "set", "auto-sync", "false")
	if !waitForLog(t, c, "0 started, 1 stopped", 20*time.Second) {
		raw, _ := os.ReadFile(filepath.Join(c.home, ".opensave", "opensave.log"))
		t.Errorf("turning auto-sync off did not stop the running daemon's watch:\n%s", raw)
	}
}

// Untracking must do the same, so a removed game is not still being watched
// (and snapshotted) by a process that has not noticed it is gone.
func TestCLIDaemonReload_RemovingAGameStopsTheWatch(t *testing.T) {
	c := newCLI(t)
	c.startDaemon()

	dir := c.saveDir("goner", map[string]string{"save.sav": "start"})
	c.mustRun("add", "Goner", dir)
	if !waitForLog(t, c, "watch list reconciled", 20*time.Second) {
		t.Fatal("the daemon never picked up the added game")
	}

	c.mustRun("remove", "goner", "--yes")
	if !waitForLog(t, c, "0 started, 1 stopped", 25*time.Second) {
		raw, _ := os.ReadFile(filepath.Join(c.home, ".opensave", "opensave.log"))
		t.Errorf("removing a game left the running daemon watching it:\n%s", raw)
	}
}

// Tracking a game takes a first snapshot of whatever is already there — the
// state before you started playing, and the one most worth having.
//
// It runs in the background so the desktop app stays responsive, which left
// the CLI racing it: `opensave add` returned as soon as the game was
// recorded, the process exited, and the half-taken snapshot went with it. The
// game ended up tracked with no history and nothing to say why. Whether it
// survived came down to how long the command happened to take.
func TestCLIDaemonReload_AddingAGameAlwaysCapturesItsFirstSnapshot(t *testing.T) {
	for i := 0; i < 5; i++ {
		c := newCLI(t)
		dir := c.saveDir("firstsnap", map[string]string{"slot1.sav": "before playing"})
		c.mustRun("add", "First Snap", dir)

		ids := c.snapshotIDs("first-snap")
		if len(ids) != 1 {
			t.Fatalf("run %d: tracking a game left %d snapshots, want exactly 1 "+
				"(the initial one): %v", i, len(ids), ids)
		}
		if out := c.mustRun("snapshots", "first-snap"); !strings.Contains(out, "Initial snapshot") {
			t.Fatalf("run %d: the snapshot taken at tracking time is not the initial one:\n%s", i, out)
		}
	}
}

// With no daemon running there is nothing to notify, and the attempt must not
// turn a change that did succeed into a failure.
func TestCLIDaemonReload_WithoutARunningDaemonTheCommandStillSucceeds(t *testing.T) {
	c := newCLI(t)
	dir := c.saveDir("nodaemon", map[string]string{"save.sav": "start"})
	out := c.mustRun("add", "No Daemon", dir)
	if strings.Contains(strings.ToLower(out), "error") {
		t.Errorf("adding a game without a running daemon reported an error:\n%s", out)
	}
	if !strings.Contains(c.mustRun("status"), "No Daemon") {
		t.Error("the game was not tracked when no daemon was running")
	}
}
