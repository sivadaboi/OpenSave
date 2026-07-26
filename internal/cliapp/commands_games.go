package cliapp

import (
	"fmt"
	"os"
	"path/filepath"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/opensave/opensave/internal/daemon"
)

// Per-game configuration and history management — everything the desktop
// app's Configuration and Manage tabs can do. Without these the CLI could
// track a game but never change how it behaves, which made features like
// App-ID matching unreachable from a headless install.

// cmdGame edits one game's settings.
func cmdGame(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 2 || args[1] != "set" {
		fmt.Fprintln(os.Stderr, gameUsage)
		return 1
	}
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, gameUsage)
		return 1
	}

	gameID, key, value := args[0], args[2], args[3]
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return fail(asJSON, unknownGameError(d, gameID, err))
	}

	switch key {
	case "name":
		game.Name = value
	case "app-id":
		game.AppID = value
	case "exe-path":
		abs, err := filepath.Abs(value)
		if err != nil {
			return fail(asJSON, err)
		}
		game.ExePath = abs
	case "cover-url":
		game.CoverURL = value
	case "auto-sync":
		game.AutoSync = isTruthy(value)
	case "max-snapshots":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fail(asJSON, fmt.Errorf("max-snapshots must be a non-negative number"))
		}
		game.MaxSnapshots = n
	case "path":
		// Relocating a save is the one change that needs validating: pointing
		// a game at a bad path would break sync and snapshots silently.
		abs, err := d.ValidateSavePath(value)
		if err != nil {
			return fail(asJSON, err)
		}
		game.SavePath = abs
	default:
		return fail(asJSON, fmt.Errorf("unknown setting %q\n\n%s", key, gameUsage))
	}

	if err := d.Store.UpdateGame(game); err != nil {
		return fail(asJSON, err)
	}
	// Re-watch so a path or auto-sync change takes effect immediately rather
	// than at the next restart.
	d.Watcher.Unwatch(game.ID)
	if game.AutoSync {
		if err := d.Watcher.Watch(game.ID, game.SavePath); err != nil {
			d.Log.Log("warn", "re-watch after config change failed: "+err.Error())
		}
	}

	if asJSON {
		return emitJSON(map[string]any{"game": game.ID, "set": key, "value": value})
	}
	success("%s %s = %s", bold(game.Name), faint(key), accent(value))
	return 0
}

const gameUsage = `usage: opensave game <gameId> set <key> <value>

  name <text>            Display name (also how peers match this game)
  path <dir|file>        Move tracking to a different save location
  app-id <steam-id>      Steam App ID, used for cover art and cross-device matching
  exe-path <file>        Executable, so the app can launch the game
  cover-url <url>        Custom cover image
  auto-sync <true|false> Watch this save and sync it automatically
  max-snapshots <n>      Snapshots kept per branch (0 = unlimited)`

func isTruthy(s string) bool {
	switch strings.ToLower(s) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// cmdUntrackAll clears the tracked list, the CLI counterpart of the app's
// "Reset tracking". Snapshot archives on disk are kept.
func cmdUntrackAll(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	games, err := d.Store.ListGames()
	if err != nil {
		return fail(asJSON, err)
	}
	if len(games) == 0 {
		if asJSON {
			return emitJSON(map[string]any{"untracked": 0})
		}
		note("Nothing is tracked.")
		return 0
	}

	// Destructive enough to deserve a speed bump when a human is driving.
	confirmed := false
	for _, a := range args {
		if a == "--yes" || a == "-y" {
			confirmed = true
		}
	}
	if !confirmed {
		if asJSON {
			return fail(asJSON, fmt.Errorf("refusing to untrack %d game(s) without --yes", len(games)))
		}
		warning("This will untrack all %d game(s).", len(games))
		note("Save files and snapshot archives on disk are kept.")
		hint("opensave untrack-all --yes")
		return 1
	}

	n := 0
	for _, g := range games {
		if err := d.UntrackGame(g.ID); err != nil {
			d.Log.Log("warn", fmt.Sprintf("untrack %q failed: %v", g.ID, err))
			continue
		}
		n++
	}
	if asJSON {
		return emitJSON(map[string]any{"untracked": n})
	}
	success("Untracked %d game(s).", n)
	note("Snapshots on disk were kept.")
	hint("opensave scan     re-add them from the correct locations")
	return 0
}

// cmdPrune applies retention limits, deleting snapshots beyond them.
func cmdPrune(args []string) int {
	asJSON, args := jsonFlag(args)
	applyDefault := false
	for _, a := range args {
		if a == "--apply-default" {
			applyDefault = true
		}
	}

	rawResp, err := daemonRequest("POST", "/api/snapshots/prune",
		map[string]any{"applyDefaultToAll": applyDefault})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(rawResp)
	}
	var res struct {
		Removed    int   `json:"removed"`
		FreedBytes int64 `json:"freedBytes"`
	}
	_ = json.Unmarshal(rawResp, &res)
	removed, freed := res.Removed, res.FreedBytes
	if removed == 0 {
		success("Nothing to prune — every game is within its limit.")
		return 0
	}
	success("Removed %d snapshot(s), freed %s", removed, bold(humanBytes(freed)))
	return 0
}

// cmdSnapshotDelete removes a single snapshot and its archive.
func cmdSnapshotDelete(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opensave snapshot-delete <gameId> <snapshotId>")
		return 1
	}
	rawResp, err := daemonRequest("DELETE",
		"/api/games/"+args[0]+"/snapshot/"+args[1], nil)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(rawResp)
	}
	var res struct {
		FreedBytes int64 `json:"freedBytes"`
	}
	_ = json.Unmarshal(rawResp, &res)
	freed := res.FreedBytes
	success("Deleted %s", accent(args[1]))
	note("freed " + humanBytes(freed))
	return 0
}

// cmdBranchDelete removes a branch and its snapshots. "main" is protected,
// matching the app.
func cmdBranchDelete(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opensave branch-delete <gameId> <branch>")
		return 1
	}
	gameID, branch := args[0], args[1]
	if strings.EqualFold(branch, "main") {
		return fail(asJSON, fmt.Errorf("the main branch can't be deleted"))
	}
	if _, err := daemonRequest("DELETE",
		"/api/games/"+gameID+"/branch/"+branch, nil); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{"game": gameID, "deletedBranch": branch})
	}
	success("Deleted branch %s", accent(branch))
	return 0
}

// cmdScanPath manages the extra folders auto-scan looks in — the positive
// counterpart to `exclude`, which existed without it.
func cmdScanPath(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, scanPathUsage)
		return 1
	}
	settings, err := d.Store.GetSettings()
	if err != nil {
		return fail(asJSON, err)
	}

	switch args[0] {
	case "list":
		if asJSON {
			paths := settings.CustomScanPaths
			if paths == nil {
				paths = []string{}
			}
			return emitJSON(paths)
		}
		section("Extra scan folders")
		if len(settings.CustomScanPaths) == 0 {
			note("None. Auto-scan checks Steam, emulators and the save database.")
			hint("opensave scanpath add <dir>")
			fmt.Println()
			return 0
		}
		for _, p := range settings.CustomScanPaths {
			fmt.Printf("  %s %s\n", symBullet(), p)
		}
		fmt.Println()
		return 0

	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave scanpath add <dir>")
			return 1
		}
		abs, err := filepath.Abs(args[1])
		if err != nil {
			return fail(asJSON, err)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return fail(asJSON, fmt.Errorf("%s isn't a folder", abs))
		}
		for _, existing := range settings.CustomScanPaths {
			if strings.EqualFold(existing, abs) {
				if asJSON {
					return emitJSON(map[string]any{"added": false, "reason": "already listed"})
				}
				note(abs + " is already in the list.")
				return 0
			}
		}
		settings.CustomScanPaths = append(settings.CustomScanPaths, abs)
		if err := d.Store.UpdateSettings(settings); err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitJSON(map[string]any{"added": true, "path": abs})
		}
		success("Auto-scan will also look in %s", abs)
		return 0

	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave scanpath remove <dir>")
			return 1
		}
		abs, _ := filepath.Abs(args[1])
		kept := settings.CustomScanPaths[:0:0]
		removed := false
		for _, p := range settings.CustomScanPaths {
			if strings.EqualFold(p, abs) || strings.EqualFold(p, args[1]) {
				removed = true
				continue
			}
			kept = append(kept, p)
		}
		if !removed {
			return fail(asJSON, fmt.Errorf("%q isn't in the scan list", args[1]))
		}
		settings.CustomScanPaths = kept
		if err := d.Store.UpdateSettings(settings); err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitJSON(map[string]any{"removed": true, "path": args[1]})
		}
		success("No longer scanning %s", args[1])
		return 0

	default:
		fmt.Fprintln(os.Stderr, scanPathUsage)
		return 1
	}
}

const scanPathUsage = `usage:
  opensave scanpath list           Extra folders auto-scan looks in
  opensave scanpath add <dir>      Add one (each subfolder becomes a candidate)
  opensave scanpath remove <dir>   Stop scanning it`

// cmdLaunch starts a tracked game through its configured executable.
func cmdLaunch(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave launch <gameId>")
		return 1
	}
	raw, err := daemonRequest("POST", "/api/games/"+args[0]+"/launch", map[string]any{})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	success("Launched %s", bold(args[0]))
	return 0
}
