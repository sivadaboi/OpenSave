package cliapp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opensave/opensave/internal/config"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/version"
)

// Running `opensave` with no arguments shows what the system is doing right
// now, not documentation. Someone typing the bare command almost always wants
// to know "is this working?" — the full command list is one `--help` away.

// Deliberately no ASCII-art logo. The panel is built from the same section
// rules, label rows and hint arrows every other command uses, so it reads as
// part of this CLI rather than borrowing another tool's look.

// overviewState is everything the panel reports, gathered before printing so
// the layout can align around it.
type overviewState struct {
	Version    string `json:"version"`
	DaemonUp   bool   `json:"daemonRunning"`
	DaemonAddr string `json:"daemonAddress"`
	DeviceName string `json:"deviceName"`
	Games      int    `json:"games"`
	Peers      int    `json:"peersOnline"`
	PeersTotal int    `json:"peersPaired"`
	Conflicts  int    `json:"conflicts"`
	RelayRoom  string `json:"relayRoom"`
}

func gatherOverview() overviewState {
	st := overviewState{Version: version.Version}
	st.DaemonAddr, _ = daemonBaseURL()

	// Prefer the running daemon: it knows live peer state the database can't.
	if raw, err := daemonRequest("GET", "/api/status", nil); err == nil {
		var payload struct {
			GameCount     int `json:"gameCount"`
			PeerCount     int `json:"peerCount"`
			ConflictCount int `json:"conflictCount"`
			Settings      struct {
				DeviceName string `json:"deviceName"`
				SyncCode   string `json:"syncCode"`
			} `json:"settings"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			st.DaemonUp = true
			st.Games = payload.GameCount
			st.PeersTotal = payload.PeerCount
			st.Conflicts = payload.ConflictCount
			st.DeviceName = payload.Settings.DeviceName
			st.RelayRoom = payload.Settings.SyncCode
		}
		if raw, err := daemonRequest("GET", "/api/peers", nil); err == nil {
			if p, err := decodePeersPayload(raw); err == nil {
				for _, peer := range p.Peers {
					if peer.Status == "online" {
						st.Peers++
					}
				}
			}
		}
		return st
	}

	// No daemon — read what we can straight from the database, so the panel
	// still tells you something useful instead of just "offline".
	paths, err := config.Resolve()
	if err != nil {
		return st
	}
	s, err := store.Open(paths.SQLiteDB)
	if err != nil {
		return st
	}
	defer s.Close()

	if games, err := s.ListGames(); err == nil {
		st.Games = len(games)
	}
	if peers, err := s.ListPeers(); err == nil {
		st.PeersTotal = len(peers)
	}
	if settings, err := s.GetSettings(); err == nil {
		st.DeviceName = settings.DeviceName
		st.RelayRoom = settings.SyncCode
	}
	return st
}

func cmdOverview(args []string) int {
	asJSON, _ := jsonFlag(args)
	st := gatherOverview()
	if asJSON {
		return emitJSON(st)
	}

	device := st.DeviceName
	if device == "" {
		device = faint("unnamed")
	}

	fmt.Println()
	if showBanner() {
		printBanner()
		fmt.Printf("\n  %s   %s\n\n",
			heading(st.Version), faint("peer-to-peer game save sync"))
	} else {
		// Narrow terminal or piped output — the wordmark would wrap or turn
		// into mojibake, so use the same title-and-rule the sections use.
		title := "OpenSave " + st.Version
		fmt.Printf("  %s   %s\n", heading(title), faint("peer-to-peer game save sync"))
		fmt.Printf("  %s\n\n", faint(strings.Repeat(sym("─", "-"), displayWidth(title)+32)))
	}

	// State as label rows, matching `daemon status` and `relay status`.
	field("daemon", daemonSummary(st))
	field("device", device)
	field("games", gamesValue(st))
	field("peers", peersSummary(st))
	field("relay", relaySummary(st))
	if st.Conflicts > 0 {
		field("conflicts", warnText(fmt.Sprintf("%d waiting on a decision", st.Conflicts)))
	}

	printQuickCommands(st)
	return 0
}

// Each summary reads as a sentence fragment rather than a value in a status
// column — the state is the text, not a separate indicator lane.

func daemonSummary(st overviewState) string {
	if !st.DaemonUp {
		return faint("not running")
	}
	return okText("running") + faint("  on "+strings.TrimPrefix(st.DaemonAddr, "http://"))
}

func gamesValue(st overviewState) string {
	if st.Games == 0 {
		return faint("none tracked yet")
	}
	return fmt.Sprintf("%d tracked", st.Games)
}

func peersSummary(st overviewState) string {
	switch {
	case st.PeersTotal == 0:
		return faint("no devices paired")
	case st.Peers == 0:
		return fmt.Sprintf("%d paired, %s", st.PeersTotal, faint("none online"))
	default:
		return fmt.Sprintf("%d paired, %s", st.PeersTotal, okText(fmt.Sprintf("%d online", st.Peers)))
	}
}

func relaySummary(st overviewState) string {
	if st.RelayRoom == "" {
		return faint("not joined")
	}
	return okText("joined") + faint("  room ") + accent(st.RelayRoom)
}

// printQuickCommands shows the handful of commands that make sense given the
// current state, rather than the full reference.
func printQuickCommands(st overviewState) {
	group := func(title string, lines [][2]string) {
		fmt.Println()
		fmt.Printf("  %s\n", faint(strings.ToUpper(title)))
		for _, l := range lines {
			fmt.Printf("    %s  %s %s\n",
				faint(sym("→", "->")), padRight(accent(l[0]), 27), faint(l[1]))
		}
	}

	if !st.DaemonUp {
		group("Start here", [][2]string{
			{"opensave daemon start", "run the sync service"},
			{"opensave service install", "run it automatically on login"},
		})
	}
	if st.Games == 0 {
		group("Find your saves", [][2]string{
			{"opensave scan", "auto-detect installed games"},
			{"opensave add <name> <path>", "track a folder yourself"},
		})
	} else {
		group("Saves", [][2]string{
			{"opensave status", "what's tracked here"},
			{"opensave sync --all", "sync everything now"},
			{"opensave scan", "look for new games"},
		})
	}
	if st.PeersTotal == 0 {
		group("Add a device", [][2]string{
			{"opensave pair <address>", "same network"},
			{"opensave relay join <code>", "different networks"},
		})
	} else {
		group("Devices", [][2]string{
			{"opensave peers", "paired and discovered devices"},
			{"opensave pair requests", "approve an incoming request"},
		})
	}
	if st.Conflicts > 0 {
		group("Needs a decision", [][2]string{
			{"opensave conflicts", "what diverged, and which side is newer"},
		})
	}

	fmt.Println()
	fmt.Printf("    %s  %s %s\n",
		faint(sym("→", "->")), padRight(accent("opensave --help"), 27), faint("every command"))
	fmt.Println()
}
