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


// Run dispatches CLI arguments; returns a process exit code.
func Run(args []string) int {
	// Bare `opensave` shows what the system is doing right now. Someone
	// typing the command with no arguments wants to know whether it's
	// working, not to read the manual — that's what --help is for.
	if len(args) == 0 {
		return cmdOverview(nil)
	}

	// `opensave --json` on its own means "the overview, machine-readable" —
	// the flag is documented as working on every command, so it has to work
	// on the default one too.
	if len(args) == 1 && args[0] == "--json" {
		return cmdOverview(args)
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
	case "conflicts":
		return cmdConflicts(rest)
	case "resolve":
		return cmdResolve(rest)
	case "backup":
		return cmdBackup(rest)
	case "prune":
		return cmdPrune(rest)
	case "snapshot-delete":
		return cmdSnapshotDelete(rest)
	case "branch-delete":
		return cmdBranchDelete(rest)
	case "launch":
		return cmdLaunch(rest)
	case "cloud":
		return cmdCloud(rest)
	case "files":
		return cmdFiles(rest)
	case "probe":
		return cmdProbe(rest)
	case "forget":
		return cmdForget(rest)
	case "service":
		return cmdService(rest)
	case "completion":
		return cmdCompletion(rest)
	case "version", "--version", "-v":
		return cmdVersion(rest)
	case "help", "--help", "-h":
		printUsage()
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
	case "game":
		return cmdGame(d, rest)
	case "scanpath":
		return cmdScanPath(d, rest)
	case "untrack-all":
		return cmdUntrackAll(d, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		printUsage()
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
		section("Auto-scan")
		note("No game saves detected.")
		hint("opensave add <name> <path>     track a folder yourself")
		fmt.Println()
		return 0
	}

	// Grouped by kind, because a flat list of 250 entries is unreadable.
	byType := map[string][]presets.DiscoveredSave{}
	for _, f := range found {
		byType[f.Type] = append(byType[f.Type], f)
	}
	labels := []struct{ kind, title string }{
		{"game", "Games"}, {"emulator", "Emulators"}, {"repack", "Repacks"},
	}

	section(fmt.Sprintf("Auto-scan %s %d save location(s)", symDot(), len(found)))
	for _, l := range labels {
		list := byType[l.kind]
		if len(list) == 0 {
			continue
		}
		fmt.Printf("\n  %s %s\n", faint(strings.ToUpper(l.title)), faint(fmt.Sprintf("(%d)", len(list))))
		for _, f := range list {
			fmt.Printf("    %s %s\n", symBullet(), bold(f.Name))
			fmt.Printf("        %s\n", faint(f.SavePath))
		}
	}
	hint("opensave add <name> <path>     track one of these")
	fmt.Println()
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
	success("Now tracking %s", bold(game.Name))
	note("id:   " + game.ID)
	note("path: " + game.SavePath)
	return 0
}

func cmdStatus(d *daemon.Daemon) int {
	games, err := d.Store.ListGames()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if len(games) == 0 {
		section("Tracked games")
		note("Nothing tracked yet.")
		hint("opensave scan", "opensave add <name> <path>")
		fmt.Println()
		return 0
	}

	section(fmt.Sprintf("Tracked games %s %d", symDot(), len(games)))
	for _, g := range games {
		fmt.Printf("  %s %s  %s\n", symBullet(), bold(g.Name), faint(g.ID))
		fmt.Printf("      %s\n", faint(g.SavePath))

		branches, _ := d.Store.ListBranches(g.ID)
		for _, b := range branches {
			snaps, _ := d.Store.ListSnapshots(g.ID, b)
			label := faint(b)
			if b == g.ActiveBranch {
				label = accent(b) + faint(" (active)")
			}
			fmt.Printf("      %s %s\n", padRight(label, 28),
				faint(fmt.Sprintf("%d snapshot(s)", len(snaps))))
		}
		fmt.Println()
	}

	peers, err := d.Store.ListPeers()
	if err == nil && len(peers) > 0 {
		section("Paired devices")
		t := newTable("device", "type", "address", "status")
		for _, p := range peers {
			status := faint(p.Status)
			if p.Status == "online" {
				status = okText(p.Status)
			}
			t.add(bold(p.Name), faint(p.DeviceType),
				faint(fmt.Sprintf("%s:%d", p.Address, p.Port)), status)
		}
		t.render()
		fmt.Println()
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
	success("Snapshot %s on branch %s", accent(snap.ID), bold(snap.BranchName))
	note(humanBytes(snap.SizeBytes))
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
	success("Restored %s", accent(snap.ID))
	note("taken " + snap.Timestamp)
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
