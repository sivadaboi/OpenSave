package cliapp

import (
	"fmt"
	"os"
	"strings"

	"github.com/opensave/opensave/internal/daemon"
	"github.com/opensave/opensave/internal/ignore"
)

// Per-game sync exclusions: files inside a tracked folder that should not
// travel between devices, written the way a .gitignore is.

func cmdIgnore(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, ignoreUsage)
		return 1
	}

	gameID := args[0]
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return fail(asJSON, unknownGameError(d, gameID, err))
	}
	lines := splitPatterns(game.SyncIgnore)

	if len(args) >= 2 {
		switch args[1] {
		case "add":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, ignoreUsage)
				return 1
			}
			for _, p := range args[2:] {
				if !containsPattern(lines, p) {
					lines = append(lines, p)
				}
			}
			return saveIgnore(d, asJSON, game.ID, game.Name, lines)

		case "remove":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, ignoreUsage)
				return 1
			}
			var kept []string
			for _, existing := range lines {
				drop := false
				for _, p := range args[2:] {
					if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(p)) {
						drop = true
					}
				}
				if !drop {
					kept = append(kept, existing)
				}
			}
			return saveIgnore(d, asJSON, game.ID, game.Name, kept)

		case "clear":
			return saveIgnore(d, asJSON, game.ID, game.Name, nil)

		case "test":
			// Answering "would this file sync?" without waiting to find out on
			// the other device is the difference between a rule someone trusts
			// and one they guess at.
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, ignoreUsage)
				return 1
			}
			rules := ignore.Parse(game.SyncIgnore)
			results := map[string]bool{}
			for _, p := range args[2:] {
				results[p] = rules.Match(p)
			}
			if asJSON {
				return emitJSON(results)
			}
			section("Would these sync? — " + game.Name)
			for _, p := range args[2:] {
				if results[p] {
					field(p, warnText("excluded — will not sync"))
				} else {
					field(p, "syncs")
				}
			}
			fmt.Println()
			return 0
		}
	}

	// Bare game id: list.
	if asJSON {
		if lines == nil {
			lines = []string{}
		}
		return emitJSON(lines)
	}
	section("Files excluded from syncing — " + game.Name)
	if len(lines) == 0 {
		fmt.Println()
		note("Nothing is excluded; every file in the save folder syncs.")
		fmt.Println()
		return 0
	}
	for _, p := range lines {
		fmt.Println("  " + p)
	}
	fmt.Println()
	note("These files stay on the device that has them. They are still captured in snapshots.")
	fmt.Println()
	return 0
}

func saveIgnore(d *daemon.Daemon, asJSON bool, gameID, gameName string, lines []string) int {
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return fail(asJSON, err)
	}
	game.SyncIgnore = strings.Join(lines, "\n")

	// Same reset the API performs: the recorded agreement was measured over a
	// save that included files the new rules exclude, and the last-snapshot
	// hash likewise. Leaving them makes the next sync read a divergence that
	// is not there.
	if err := d.Store.RebaseAgreedHashesForGame(gameID,
		d.P2P.Sync.FilteredContentHash(gameID, game)); err != nil {
		d.Log.Log("warn", "could not reset sync agreement after an exclusion change: "+err.Error())
	}
	game.LastManifestHash = ""

	if err := d.Store.UpdateGame(game); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		if lines == nil {
			lines = []string{}
		}
		return emitJSON(lines)
	}
	success("exclusions updated for %q (%d pattern(s))", gameName, len(lines))
	note("Set the same patterns on your other devices — each one applies its own.")
	return 0
}

func splitPatterns(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

func containsPattern(lines []string, p string) bool {
	for _, existing := range lines {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(p)) {
			return true
		}
	}
	return false
}

const ignoreUsage = `usage:
  opensave ignore <gameId>                     List a game's excluded files
  opensave ignore <gameId> add <pattern>...    Stop syncing files matching a pattern
  opensave ignore <gameId> remove <pattern>... Sync them again
  opensave ignore <gameId> clear               Remove every pattern
  opensave ignore <gameId> test <path>...      Check whether a path would sync

Some games keep their save and their device-specific settings in the same
folder, where syncing the settings can break the game on the other machine.
Patterns are written like a .gitignore:

  Config.gs        a file of that name, at any depth
  /Config.gs       only at the top of the save folder
  *.log            by extension
  logs/            a folder and everything in it
  !keep.log        an exception to an earlier pattern

Matching ignores case. Set the same patterns on your other devices — each
applies its own, and a device without them is unaffected.

Excluded files are still captured in snapshots and restored by rollbacks;
these rules decide what SYNCS, nothing else.`
