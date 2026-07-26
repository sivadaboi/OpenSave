// Package cliapp implements the opensave command-line interface: direct
// offline operations against the local database, plus daemon start for
// headless operation — porting bin/opensave.js.
package cliapp

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/opensave/opensave/internal/api"
	"github.com/opensave/opensave/internal/daemon"
	"github.com/opensave/opensave/internal/presets"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/sysintegration/upnp"
)

const usage = `OpenSave — P2P game save sync

No account, no server, no quota. Devices sync directly with each other.

Games:
  opensave scan                          Auto-detect game saves on this machine
  opensave add <name> <path>             Track a game save folder/file
  opensave remove <gameId>               Stop tracking a game
  opensave status                        Show tracked games, branches, peers

Sync:
  opensave sync [<gameId>|--all]         Sync now (all games by default)
  opensave peers                         List paired devices
  opensave pair <host[:port]>            Ask a device on your LAN to pair
  opensave pair requests|approve|reject  Handle incoming pairing requests
  opensave unpair <peerId>               Drop a paired device
  opensave relay status|join|leave       Internet sync between networks

History:
  opensave snapshot <gameId> [comment]   Create a snapshot
  opensave snapshots <gameId>            List snapshots
  opensave rollback <gameId> <snapId>    Restore a snapshot
  opensave branch <gameId> <name>        Create a branch
  opensave checkout <gameId> <name>      Switch branch
  opensave export <gameId> <dir>         Copy the current save out to a folder

Configuration:
  opensave config [set <key> <value>]    Read or change settings
  opensave exclude list|add|remove       Folders auto-scan should skip
  opensave link <gameId> <otherId>       Treat two tracked games as the same
  opensave unlink <aliasId>              Undo a link
  opensave links <gameId>                Show ids linked to a game

Service:
  opensave daemon start [--port N]       Run the daemon (REST API + watcher)
  opensave daemon status                 Is a daemon running, and where
  opensave daemon stop                   Stop the running daemon
  opensave service install|uninstall     Install a systemd --user service (Linux)
  opensave completion bash|zsh|fish      Shell completion script
  opensave upnp <port> [--delete]        Forward (or remove) a router port via UPnP
  opensave version                       Print the version

Add --json to any command for machine-readable output.
`

// Run dispatches CLI arguments; returns a process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 0
	}

	cmd, rest := args[0], args[1:]

	// These talk to the running daemon (or manage it) rather than opening the
	// database, so they must not construct a second daemon — doing so would
	// contend for the same SQLite file and the same port.
	switch cmd {
	case "daemon":
		return cmdDaemon(rest)
	case "upnp":
		return cmdUpnp(rest)
	case "sync":
		return cmdSync(rest)
	case "peers":
		return cmdPeers(rest)
	case "pair":
		return cmdPair(rest)
	case "unpair":
		return cmdUnpair(rest)
	case "relay":
		return cmdRelay(rest)
	case "service":
		return cmdService(rest)
	case "completion":
		return cmdCompletion(rest)
	case "version", "--version", "-v":
		return cmdVersion(rest)
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	}

	d, err := daemon.New(daemon.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer d.Stop()

	switch cmd {
	case "scan":
		return cmdScan(d)
	case "add":
		return cmdAdd(d, rest)
	case "status":
		return cmdStatus(d)
	case "snapshot":
		return cmdSnapshot(d, rest)
	case "rollback":
		return cmdRollback(d, rest)
	case "branch":
		return cmdBranch(d, rest)
	case "checkout":
		return cmdCheckout(d, rest)
	case "remove":
		return cmdRemove(d, rest)
	case "snapshots":
		return cmdSnapshots(d, rest)
	case "export":
		return cmdExport(d, rest)
	case "exclude":
		return cmdExclude(d, rest)
	case "link":
		return cmdLink(d, rest)
	case "unlink":
		return cmdUnlinkGame(d, rest)
	case "links":
		return cmdLinks(d, rest)
	case "config":
		return cmdConfig(d, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 1
	}
}

func runDaemon(args []string) int {
	port := 0 // resolved from settings below
	for i := 0; i < len(args); i++ {
		if args[i] == "--port" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &port)
			i++
		}
	}

	d, err := daemon.New(daemon.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer d.Stop()

	if port == 0 {
		settings, err := d.Store.GetSettings()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		port = settings.Port
	}

	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	server := api.New(d)
	addr, err := server.Start(port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer server.Stop()

	// Record the PID so `opensave daemon stop` can find *this* daemon. Only
	// the CLI writes it: the desktop app serves the same API, and stopping it
	// out from under its window would look like a crash.
	pidPath := daemonPIDPath(d.Paths.HomeDir)
	_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o666)
	defer os.Remove(pidPath)

	fmt.Printf("OpenSave daemon listening on http://%s\n", addr)
	fmt.Println("Press Ctrl+C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nshutting down...")
	return 0
}

// daemonPIDPath is where a CLI-started daemon records its process id.
func daemonPIDPath(homeDir string) string {
	return filepath.Join(homeDir, "daemon.pid")
}

func cmdUpnp(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: opensave upnp <port> [--delete]")
		return 1
	}
	port := 0
	fmt.Sscanf(args[0], "%d", &port)
	if port <= 0 || port > 65535 {
		fmt.Fprintf(os.Stderr, "invalid port %q\n", args[0])
		return 1
	}
	remove := len(args) > 1 && args[1] == "--delete"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if remove {
		if err := upnp.Remove(ctx, port); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("Removed UPnP mapping for port %d\n", port)
		return 0
	}

	externalIP, err := upnp.Forward(ctx, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Port %d forwarded via UPnP", port)
	if externalIP != "" {
		fmt.Printf(" — external address %s:%d", externalIP, port)
	}
	fmt.Println()
	return 0
}

func cmdScan(d *daemon.Daemon) int {
	settings, err := d.Store.GetSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	found := d.Scanner.Scan(settings.CustomScanPaths)
	found = presets.FilterExcluded(found, settings.ExcludePaths)
	if len(found) == 0 {
		fmt.Println("No game saves auto-detected.")
		return 0
	}
	fmt.Printf("Detected %d save location(s):\n\n", len(found))
	for _, f := range found {
		appID := f.AppID
		if appID == "" {
			appID = "-"
		}
		fmt.Printf("  [%s] %s\n      path: %s  (appId: %s)\n", f.Type, f.Name, f.SavePath, appID)
	}
	fmt.Println("\nTrack one with: opensave add <name> <path>")
	return 0
}

func cmdAdd(d *daemon.Daemon, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opensave add <name> <path>")
		return 1
	}
	name, savePath := args[0], args[1]
	abs, err := filepath.Abs(savePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	game, err := d.TrackGame(store.Game{Name: name, SavePath: abs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Tracking %q (id: %s)\n  save path: %s\n", game.Name, game.ID, game.SavePath)
	return 0
}

func cmdStatus(d *daemon.Daemon) int {
	games, err := d.Store.ListGames()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Printf("Tracked games: %d\n\n", len(games))
	for _, g := range games {
		fmt.Printf("  %s (%s)\n    save path: %s\n", g.Name, g.ID, g.SavePath)
		branches, _ := d.Store.ListBranches(g.ID)
		for _, b := range branches {
			snaps, _ := d.Store.ListSnapshots(g.ID, b)
			marker := " "
			if b == g.ActiveBranch {
				marker = "*"
			}
			fmt.Printf("    %s branch %-12s %d snapshot(s)\n", marker, b, len(snaps))
		}
	}

	peers, err := d.Store.ListPeers()
	if err == nil {
		fmt.Printf("\nPaired peers: %d\n", len(peers))
		for _, p := range peers {
			fmt.Printf("  %s (%s) — %s:%d [%s]\n", p.Name, p.DeviceType, p.Address, p.Port, p.Status)
		}
	}
	return 0
}

func cmdSnapshot(d *daemon.Daemon, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: opensave snapshot <gameId> [comment]")
		return 1
	}
	comment := ""
	if len(args) > 1 {
		comment = strings.Join(args[1:], " ")
	}

	snap, err := d.Snapshots.Create(args[0], comment, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Created snapshot %s (%.1f KB) on branch %s\n", snap.ID, float64(snap.SizeBytes)/1024, snap.BranchName)
	return 0
}

func cmdRollback(d *daemon.Daemon, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opensave rollback <gameId> <snapshotId>")
		return 1
	}
	snap, err := d.Snapshots.Restore(args[0], args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Restored snapshot %s (from %s)\n", snap.ID, snap.Timestamp)
	return 0
}

func cmdBranch(d *daemon.Daemon, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opensave branch <gameId> <name>")
		return 1
	}
	clean, err := d.Snapshots.CreateBranch(args[0], args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Created branch %q\n", clean)
	return 0
}

func cmdCheckout(d *daemon.Daemon, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opensave checkout <gameId> <name>")
		return 1
	}
	if err := d.Snapshots.SwitchBranch(args[0], args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("Switched to branch %q\n", args[1])
	return 0
}

func cmdRemove(d *daemon.Daemon, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: opensave remove <gameId>")
		return 1
	}
	if err := d.UntrackGame(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("No longer tracking %q (snapshot files kept on disk)\n", args[0])
	return 0
}
