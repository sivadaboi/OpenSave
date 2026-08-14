package cliapp

import (
	"fmt"
	"os"

	"github.com/opensave/opensave/internal/daemon"
)

// A game's extra save locations — the folders beyond its main one that belong
// to the same title, for a save split between (say) AppData and Documents.

func cmdLocations(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, locationsUsage)
		return 1
	}

	switch args[0] {
	case "add":
		if len(args) < 4 {
			fmt.Fprintln(os.Stderr, locationsUsage)
			return 1
		}
		gameID, name, path := args[1], args[2], args[3]
		abs, err := d.CheckRestoreTarget(path)
		if err != nil {
			return fail(asJSON, err)
		}
		if err := d.Store.AddGameRoot(gameID, name, abs); err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitJSON(map[string]any{"game": gameID, "location": name, "path": abs})
		}
		success("%q now also covers %s", gameID, abs)
		note("The other device needs a location with the same name for it to sync.")
		return 0

	case "remove":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, locationsUsage)
			return 1
		}
		if err := d.Store.RemoveGameRoot(args[1], args[2]); err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitJSON(map[string]any{"removed": args[2]})
		}
		success("stopped covering the %q location of %q", args[2], args[1])
		note("Its files are untouched — this stops tracking the folder, it does not delete anything.")
		return 0
	}

	// Bare game id: list.
	//
	// Only bare. Anything after the id is refused rather than dropped: the
	// subcommand comes before the game here, and putting it after is the
	// natural mistake — "locations <game> add config <path>" reads fine and
	// used to print that game's locations, discarding the other three
	// arguments and reporting nothing wrong. The listing looks like success,
	// so the location silently never existed. Exactly the fault the relay had
	// when it accepted arguments and ignored them.
	if len(args) > 1 {
		fmt.Fprintf(os.Stderr, "opensave locations: unexpected argument %q after the game id\n\n", args[1])
		fmt.Fprintln(os.Stderr, locationsUsage)
		return 1
	}

	gameID := args[0]
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return fail(asJSON, err)
	}
	roots, err := d.Store.ListGameRoots(gameID)
	if err != nil {
		return fail(asJSON, err)
	}

	if asJSON {
		out := []map[string]any{{"name": "", "path": game.SavePath, "primary": true, "mapped": true}}
		for _, r := range roots {
			out = append(out, map[string]any{"name": r.Name, "path": r.Path, "primary": false, "mapped": r.Mapped()})
		}
		return emitJSON(out)
	}

	section("Save locations — " + game.Name)
	field("main save", game.SavePath)
	for _, r := range roots {
		if r.Mapped() {
			field(r.Name, r.Path)
			continue
		}
		// Known from a peer or a backup, but nowhere to put it here. Saying so
		// matters: this location is being skipped by every sync and every
		// restore until someone points it somewhere.
		field(r.Name, warnText("no folder on this device — set one with `opensave locations add`"))
	}
	if len(roots) == 0 {
		fmt.Println()
		note("This game has only its main save folder.")
	}
	fmt.Println()
	return 0
}

const locationsUsage = `usage:
  opensave locations <gameId>                        List a game's save locations
  opensave locations add <gameId> <name> <path>      Add another folder to a game
  opensave locations remove <gameId> <name>          Stop covering one

A game can have its save split across several folders — data in one place,
configuration in another. Name each extra folder, and give the SAME name on
your other devices: the name is what the two sides match on, since the path
differs from machine to machine.

Removing a location leaves its files alone.`
