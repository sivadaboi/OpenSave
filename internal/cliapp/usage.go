package cliapp

import (
	"fmt"
	"sort"
	"strings"
)

// The command reference as data rather than a printed blob. Two things fall
// out of that: `--help` can be styled like every other screen instead of
// dumping unthemed text, and shell completion is derived from the same list,
// so a new command can't appear in one and be missing from the other.

type commandEntry struct {
	usage string // "sync [<gameId>|--all]"
	desc  string
}

type commandGroup struct {
	title   string
	entries []commandEntry
}

var commandGroups = []commandGroup{
	{"Games", []commandEntry{
		{"scan", "Auto-detect game saves on this machine"},
		{"add <name> <path>", "Track a game save folder or file"},
		{"remove <gameId>", "Stop tracking a game"},
		{"untrack-all --yes", "Stop tracking everything (keeps snapshots)"},
		{"game <gameId> set <key> <value>", "Per-game settings (path, app-id, auto-sync…)"},
		{"launch <gameId>", "Start the game"},
		{"status", "Tracked games, branches and peers"},
	}},
	{"Sync", []commandEntry{
		{"sync [<gameId>|--all]", "Sync now (everything by default)"},
		{"peers", "Paired, discovered and pending devices"},
		{"pair <host[:port]>", "Ask a device on your LAN to pair"},
		{"pair requests|approve|reject", "Handle incoming pairing requests"},
		{"unpair <peerId>", "Drop a paired device"},
		{"probe <host[:port]>", "Check whether a device answers"},
		{"forget <peerId>", "Remove a stale device record"},
		{"relay status|join|leave", "Internet sync between networks"},
		{"conflicts", "Saves waiting on a decision"},
		{"resolve <gameId> <choice>", "Settle a conflict"},
	}},
	{"History", []commandEntry{
		{"snapshot <gameId> [comment]", "Create a snapshot"},
		{"snapshots <gameId>", "List snapshots"},
		{"rollback <gameId> <snapId>", "Restore a snapshot"},
		{"branch <gameId> <name>", "Create a branch"},
		{"checkout <gameId> <name>", "Switch branch"},
		{"branch-delete <gameId> <name>", "Delete a branch and its snapshots"},
		{"snapshot-delete <id> <snapId>", "Delete one snapshot"},
		{"prune [--apply-default]", "Apply retention limits now"},
		{"files <gameId> <snapId> [path]", "List a snapshot's contents, or restore one file"},
		{"export <gameId> <dir>", "Copy the current save out to a folder"},
		{"backup export <file.sscb>", "Write a portable backup archive"},
		{"backup import <file.sscb>", "Read one back"},
	}},
	{"Cloud backup", []commandEntry{
		{"cloud status", "Provider and connection state"},
		{"cloud browse", "Everything stored in the cloud"},
		{"cloud list <gameId>", "Cloud snapshots for one game"},
		{"cloud push <gameId>", "Upload local snapshots"},
		{"cloud restore <id> <file>", "Pull one back"},
		{"cloud delete <gameId> --yes", "Remove a game's cloud copies"},
	}},
	{"Configuration", []commandEntry{
		{"config [set <key> <value>]", "Read or change settings"},
		{"scanpath list|add|remove", "Extra folders to auto-scan"},
		{"exclude list|add|remove", "Folders auto-scan should skip"},
		{"link <gameId> <otherId>", "Treat two tracked games as the same"},
		{"unlink <aliasId>", "Undo a link"},
		{"links <gameId>", "Show ids linked to a game"},
	}},
	{"Service", []commandEntry{
		{"daemon start [--port N]", "Run the daemon (REST API + watcher)"},
		{"daemon status", "Is a daemon running, and where"},
		{"daemon stop", "Stop a daemon started by the CLI"},
		{"service install|uninstall", "Install a systemd --user service (Linux)"},
		{"completion bash|zsh|fish", "Shell completion script"},
		{"upnp <port> [--delete]", "Forward or remove a router port via UPnP"},
		{"update [--check]", "Update this CLI to the latest release"},
		{"version", "Print the version"},
	}},
}

// printUsage renders the reference using the same styling as everything else.
func printUsage() {
	fmt.Println()
	fmt.Printf("  %s   %s\n", heading("OpenSave"), faint("peer-to-peer game save sync"))
	fmt.Printf("  %s\n", faint("No account, no server, no quota. Devices sync directly with each other."))

	// Width the command column to the longest entry so descriptions line up
	// across every group, not just within one.
	width := 0
	for _, g := range commandGroups {
		for _, e := range g.entries {
			if n := len(e.usage); n > width {
				width = n
			}
		}
	}

	for _, g := range commandGroups {
		fmt.Printf("\n  %s\n", faint(strings.ToUpper(g.title)))
		for _, e := range g.entries {
			fmt.Printf("    %s %s\n",
				padRight(accent(e.usage), width+2), faint(e.desc))
		}
	}

	fmt.Printf("\n  %s\n\n", faint("Add --json to any command for machine-readable output."))
}

// commandNames returns every top-level command word, derived from the groups
// above so completion can never drift from the documented list.
func commandNames() []string {
	seen := map[string]bool{"help": true}
	for _, g := range commandGroups {
		for _, e := range g.entries {
			name := strings.Fields(e.usage)[0]
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
