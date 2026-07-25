package cliapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/config"
	"github.com/opensave/opensave/internal/daemon"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/version"
)

// cmdDaemon dispatches the daemon sub-commands. `start` runs one in the
// foreground; the others act on a daemon that's already running.
func cmdDaemon(args []string) int {
	if len(args) == 0 {
		return runDaemon(args)
	}
	switch args[0] {
	case "start":
		return runDaemon(args[1:])
	case "status":
		return cmdDaemonStatus(args[1:])
	case "stop":
		return cmdDaemonStop(args[1:])
	default:
		// Keeps `opensave daemon --port 9000` working as before.
		return runDaemon(args)
	}
}

func cmdDaemonStatus(args []string) int {
	asJSON, _ := jsonFlag(args)

	base, _ := daemonBaseURL()
	if !daemonRunning() {
		if asJSON {
			emitJSON(map[string]any{"running": false, "address": base})
			return 1
		}
		fmt.Printf("Not running (nothing answering at %s).\n", base)
		fmt.Println("Start it with `opensave daemon start`.")
		return 1
	}

	raw, err := daemonRequest("GET", "/api/status", nil)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}

	var st struct {
		GameCount     int `json:"gameCount"`
		PeerCount     int `json:"peerCount"`
		ConflictCount int `json:"conflictCount"`
		Settings      struct {
			DeviceName string `json:"deviceName"`
		} `json:"settings"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return emitRawJSON(raw)
	}
	fmt.Printf("Running at %s\n", base)
	fmt.Printf("  device:    %s\n", st.Settings.DeviceName)
	fmt.Printf("  games:     %d\n", st.GameCount)
	fmt.Printf("  peers:     %d\n", st.PeerCount)
	if st.ConflictCount > 0 {
		fmt.Printf("  conflicts: %d — resolve them in the app or on another device\n", st.ConflictCount)
	}
	return 0
}

// cmdDaemonStop stops a daemon this CLI started. It deliberately won't touch
// one belonging to the desktop app: that app serves the same API, and killing
// its server would leave a window that looks alive but does nothing.
func cmdDaemonStop(args []string) int {
	asJSON, _ := jsonFlag(args)

	paths, err := config.Resolve()
	if err != nil {
		return fail(asJSON, err)
	}
	pidPath := filepath.Join(paths.HomeDir, "daemon.pid")

	raw, err := os.ReadFile(pidPath)
	if err != nil {
		msg := "No CLI daemon is running."
		if daemonRunning() {
			msg = "A daemon is running, but it wasn't started by this CLI — " +
				"if that's the desktop app, quit it from the tray. " +
				"If it's the systemd service, use `systemctl --user stop opensave-daemon`."
		}
		if asJSON {
			emitJSON(map[string]any{"stopped": false, "reason": msg})
			return 0
		}
		fmt.Println(msg)
		return 0
	}

	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil {
		return fail(asJSON, fmt.Errorf("unreadable %s: %w", pidPath, convErr))
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath) // stale
		return fail(asJSON, fmt.Errorf("no process %d — removed the stale pid file", pid))
	}
	if err := terminate(proc); err != nil {
		return fail(asJSON, fmt.Errorf("could not stop pid %d: %w", pid, err))
	}

	// Wait for it to actually stop, then clear anything it didn't. A killed
	// process (Windows has no SIGTERM) never runs its own cleanup, and a
	// leftover daemon.addr points every client at a dead port — the exact
	// failure that file exists to prevent.
	for i := 0; i < 20 && daemonRunning(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !daemonRunning() {
		_ = os.Remove(pidPath)
		_ = os.Remove(filepath.Join(paths.HomeDir, "daemon.addr"))
	}

	if asJSON {
		emitJSON(map[string]any{"stopped": true, "pid": pid})
		return 0
	}
	fmt.Printf("Stopped the daemon (pid %d).\n", pid)
	return 0
}

func cmdVersion(args []string) int {
	asJSON, _ := jsonFlag(args)
	if asJSON {
		return emitJSON(map[string]any{
			"version": version.Version,
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
		})
	}
	fmt.Printf("OpenSave %s (%s/%s)\n", version.Version, runtime.GOOS, runtime.GOARCH)
	return 0
}

// ── systemd service ──────────────────────────────────────────────────────

const serviceUnitName = "opensave-daemon.service"

// cmdService installs the daemon as a systemd --user service, so syncing runs
// without anyone logging into a desktop — the normal setup on a headless box
// or a Steam Deck that lives in Game Mode.
func cmdService(args []string) int {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "service management is Linux-only; use Settings → Start on boot on this platform")
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave service install|uninstall|status")
		return 1
	}

	unitDir, err := userUnitDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	unitPath := filepath.Join(unitDir, serviceUnitName)

	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.MkdirAll(unitDir, 0o777); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(unitPath, []byte(renderUnit(exe)), 0o666); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("Installed %s\n\n", unitPath)
		fmt.Println("Enable it with:")
		fmt.Println("  systemctl --user daemon-reload")
		fmt.Println("  systemctl --user enable --now opensave-daemon")
		fmt.Println()
		fmt.Println("On SteamOS, also run `sudo loginctl enable-linger $USER` so it")
		fmt.Println("keeps running across Game Mode / Desktop Mode switches.")
		return 0

	case "uninstall":
		// Stop it first, or systemd keeps running a unit whose file is gone.
		_ = exec.Command("systemctl", "--user", "disable", "--now", serviceUnitName).Run()
		if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		fmt.Println("Service removed.")
		return 0

	case "status":
		out, _ := exec.Command("systemctl", "--user", "is-enabled", serviceUnitName).CombinedOutput()
		state := strings.TrimSpace(string(out))
		if state == "" {
			state = "not installed"
		}
		fmt.Printf("%s: %s\n", serviceUnitName, state)
		if _, err := os.Stat(unitPath); err == nil {
			fmt.Printf("unit file: %s\n", unitPath)
		}
		return 0

	default:
		fmt.Fprintln(os.Stderr, "usage: opensave service install|uninstall|status")
		return 1
	}
}

func userUnitDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// renderUnit points the unit at this very binary, so it works whether the CLI
// came from a package, a tarball, or a Flatpak.
func renderUnit(exePath string) string {
	return `[Unit]
Description=OpenSave save-sync daemon
Documentation=https://github.com/sivadaboi/OpenSave
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=` + exePath + ` daemon start
Restart=on-failure
RestartSec=10

# Background sync should never compete with a running game.
Nice=10
IOSchedulingClass=best-effort
IOSchedulingPriority=6

[Install]
WantedBy=default.target
`
}

// unknownGameError turns a bare "not found" into something actionable: on a
// CLI the user has typed an id from memory, and the useful reply is which ids
// actually exist.
func unknownGameError(d *daemon.Daemon, gameID string, err error) error {
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	games, listErr := d.Store.ListGames()
	if listErr != nil || len(games) == 0 {
		return fmt.Errorf("no tracked game with id %q (nothing is tracked yet — try `opensave scan`)", gameID)
	}
	ids := make([]string, 0, len(games))
	for _, g := range games {
		ids = append(ids, g.ID)
	}
	if len(ids) > 8 {
		return fmt.Errorf("no tracked game with id %q — run `opensave status` to list %d tracked games",
			gameID, len(ids))
	}
	return fmt.Errorf("no tracked game with id %q — tracked ids: %s", gameID, strings.Join(ids, ", "))
}

// ── Local history commands ───────────────────────────────────────────────

// cmdSnapshots lists a game's snapshots on the active branch.
func cmdSnapshots(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave snapshots <gameId> [--json]")
		return 1
	}
	game, err := d.Store.GetGame(args[0])
	if err != nil {
		return fail(asJSON, unknownGameError(d, args[0], err))
	}
	branch := game.ActiveBranch
	if len(args) > 1 {
		branch = args[1]
	}
	snaps, err := d.Store.ListSnapshots(game.ID, branch)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(snaps)
	}
	if len(snaps) == 0 {
		fmt.Printf("No snapshots for %q on branch %q.\n", game.Name, branch)
		return 0
	}
	fmt.Printf("%s — branch %s, %d snapshot(s), newest first:\n\n", game.Name, branch, len(snaps))
	for _, s := range snaps {
		comment := s.Comment
		if comment == "" {
			comment = "(no comment)"
		}
		fmt.Printf("  %-22s %s  %s\n", s.ID, s.Timestamp, comment)
	}
	return 0
}

// cmdExport copies a game's current save out to a folder — the headless
// equivalent of "give me my files back", with no archive or app required.
func cmdExport(d *daemon.Daemon, args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opensave export <gameId> <destination-dir> [--json]")
		return 1
	}
	game, err := d.Store.GetGame(args[0])
	if err != nil {
		return fail(asJSON, unknownGameError(d, args[0], err))
	}
	dest := filepath.Join(args[1], sanitizeFileName(game.Name))
	copied, bytes, err := copyTree(game.SavePath, dest)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{
			"game": game.Name, "destination": dest, "files": copied, "bytes": bytes,
		})
	}
	fmt.Printf("Exported %d file(s), %s, to %s\n", copied, humanBytes(bytes), dest)
	return 0
}

// copyTree copies a file or directory verbatim — saves come out exactly as the
// game wrote them, not wrapped in an archive.
func copyTree(src, dest string) (files int, total int64, err error) {
	info, err := os.Stat(src)
	if err != nil {
		return 0, 0, fmt.Errorf("save path %q: %w", src, err)
	}
	if !info.IsDir() {
		if err := os.MkdirAll(filepath.Dir(dest), 0o777); err != nil {
			return 0, 0, err
		}
		n, err := copyFile(src, dest)
		return 1, n, err
	}
	err = filepath.Walk(src, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o777)
		}
		n, err := copyFile(path, target)
		if err != nil {
			return err
		}
		files++
		total += n
		return nil
	})
	return files, total, err
}

func copyFile(src, dest string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o777); err != nil {
		return 0, err
	}
	out, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, in)
}

func sanitizeFileName(name string) string {
	repl := strings.NewReplacer(
		"/", "-", `\`, "-", ":", "-", "*", "-", "?", "", `"`, "'", "<", "(", ">", ")", "|", "-")
	return strings.TrimSpace(repl.Replace(name))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

