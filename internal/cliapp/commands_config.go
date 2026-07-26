package cliapp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opensave/opensave/internal/daemon"
)

// Settings and game-linking management, so a headless install isn't forced
// back to the desktop app to change how scanning or matching behaves.

// cmdExclude manages the folders auto-scan skips.
func cmdExclude(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, excludeUsage)
		return 1
	}

	settings, err := d.Store.GetSettings()
	if err != nil {
		return fail(asJSON, err)
	}

	switch args[0] {
	case "list":
		if asJSON {
			paths := settings.ExcludePaths
			if paths == nil {
				paths = []string{}
			}
			return emitJSON(paths)
		}
		if len(settings.ExcludePaths) == 0 {
			fmt.Println("No excluded folders. Auto-scan looks everywhere it knows about.")
			return 0
		}
		fmt.Printf("%d excluded folder(s):\n\n", len(settings.ExcludePaths))
		for _, p := range settings.ExcludePaths {
			fmt.Printf("  %s\n", p)
		}
		return 0

	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave exclude add <path>")
			return 1
		}
		abs, err := filepath.Abs(args[1])
		if err != nil {
			return fail(asJSON, err)
		}
		for _, existing := range settings.ExcludePaths {
			if strings.EqualFold(existing, abs) {
				if asJSON {
					return emitJSON(map[string]any{"added": false, "reason": "already excluded"})
				}
				fmt.Printf("%s is already excluded.\n", abs)
				return 0
			}
		}
		settings.ExcludePaths = append(settings.ExcludePaths, abs)
		if err := d.Store.UpdateSettings(settings); err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitJSON(map[string]any{"added": true, "path": abs})
		}
		fmt.Printf("Auto-scan will skip %s\n", abs)
		return 0

	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave exclude remove <path>")
			return 1
		}
		abs, _ := filepath.Abs(args[1])
		kept := settings.ExcludePaths[:0:0]
		removed := false
		for _, p := range settings.ExcludePaths {
			if strings.EqualFold(p, abs) || strings.EqualFold(p, args[1]) {
				removed = true
				continue
			}
			kept = append(kept, p)
		}
		if !removed {
			return fail(asJSON, fmt.Errorf("%q isn't in the exclude list", args[1]))
		}
		settings.ExcludePaths = kept
		if err := d.Store.UpdateSettings(settings); err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitJSON(map[string]any{"removed": true, "path": args[1]})
		}
		fmt.Printf("No longer excluding %s\n", args[1])
		return 0

	default:
		fmt.Fprintln(os.Stderr, excludeUsage)
		return 1
	}
}

const excludeUsage = `usage:
  opensave exclude list            Folders auto-scan skips
  opensave exclude add <path>      Skip a folder (and everything inside it)
  opensave exclude remove <path>   Stop skipping it`

// cmdLink merges one tracked game into another so both ids sync to the same
// save — the same title tracked under different names on two machines.
func cmdLink(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr,
			"usage: opensave link <gameId> <otherGameId>\n"+
				"  Treats both as the same game. <otherGameId> is merged in and\n"+
				"  removed from the library; its save files are left alone.")
		return 1
	}
	canonical, alias := args[0], args[1]
	if err := d.LinkGames(canonical, alias); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{"canonical": canonical, "alias": alias})
	}
	fmt.Printf("Linked: %q now also answers to %q.\n", canonical, alias)
	return 0
}

// cmdUnlinkGame removes a link, restoring the merged game as its own entry.
func cmdUnlinkGame(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: opensave unlink <aliasGameId>")
		return 1
	}
	if err := d.UnlinkGame(args[0]); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{"unlinked": args[0]})
	}
	fmt.Printf("Unlinked %q — it's tracked separately again.\n", args[0])
	return 0
}

// cmdLinks lists the aliases pointing at a game.
func cmdLinks(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: opensave links <gameId>")
		return 1
	}
	aliases, err := d.Store.ListGameAliases(args[0])
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		if aliases == nil {
			aliases = []string{}
		}
		return emitJSON(aliases)
	}
	if len(aliases) == 0 {
		fmt.Printf("No other game is linked to %q.\n", args[0])
		return 0
	}
	fmt.Printf("%q is also known as:\n\n", args[0])
	for _, a := range aliases {
		fmt.Printf("  %s\n", a)
	}
	return 0
}

// cmdConfig reads and writes daemon settings, so a headless box can be
// configured without the desktop app.
func cmdConfig(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	settings, err := d.Store.GetSettings()
	if err != nil {
		return fail(asJSON, err)
	}

	if len(args) == 0 || args[0] == "list" {
		if asJSON {
			return emitJSON(settings)
		}
		fmt.Printf("device name:      %s\n", settings.DeviceName)
		fmt.Printf("port:             %d\n", settings.Port)
		fmt.Printf("relay:            %s\n", settings.RelayURL)
		fmt.Printf("relay room:       %s\n", orNone(settings.SyncCode))
		fmt.Printf("match by app id:  %v\n", settings.MatchByAppID)
		fmt.Printf("snapshot limit:   %d\n", settings.DefaultMaxSnapshots)
		fmt.Printf("data dir:         %s\n", settings.DataDir)
		fmt.Printf("snapshots dir:    %s\n", settings.BackupsDir)
		return 0
	}

	if args[0] != "set" || len(args) < 3 {
		fmt.Fprintln(os.Stderr, configUsage)
		return 1
	}

	key, value := args[1], args[2]
	switch key {
	case "device-name":
		settings.DeviceName = value
	case "match-by-app-id":
		settings.MatchByAppID = value == "true" || value == "yes" || value == "1"
	case "snapshot-limit":
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 0 {
			return fail(asJSON, fmt.Errorf("snapshot-limit must be a non-negative number"))
		}
		settings.DefaultMaxSnapshots = n
	case "relay-url":
		settings.RelayURL = value
	default:
		return fail(asJSON, fmt.Errorf("unknown setting %q\n\n%s", key, configUsage))
	}

	if err := d.Store.UpdateSettings(settings); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{"set": key, "value": value})
	}
	fmt.Printf("%s = %s\n", key, value)
	if daemonRunning() {
		fmt.Println("Restart the daemon for this to take effect.")
	}
	return 0
}

const configUsage = `usage:
  opensave config [list]                    Show current settings
  opensave config set device-name <name>    How other devices see this one
  opensave config set match-by-app-id <t/f> Link same-App-ID games across devices
  opensave config set snapshot-limit <n>    Snapshots kept per branch (0 = all)
  opensave config set relay-url <url>       Relay server for internet sync`

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

var _ = json.Marshal // settings marshal through emitJSON
