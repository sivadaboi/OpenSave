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

// osMark is the OpenSave wordmark in block art: "OS", the same letters as the
// app icon. Rendered beside the status rows.
var osMark = []string{
	"██████  ██████",
	"██  ██  ██    ",
	"██  ██  ██████",
	"██  ██      ██",
	"██████  ██████",
}

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

// statusDot renders an on/off indicator: filled when live, hollow when not.
func statusDot(on bool, text string) string {
	if on {
		return okText(sym("● ", "* ")) + text
	}
	return faint(sym("○ ", "- ") + text)
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

	// Three aligned columns — label, value, state — so the indicators line up
	// down the panel instead of drifting with the text beside them.
	type row struct{ label, value, state string }
	rows := []row{
		{"", heading("OpenSave"), ""},
		{"", faint("peer-to-peer game save sync"), ""},
		{},
		{"cli", accent(st.Version), statusDot(true, "ready")},
		{"daemon", daemonValue(st), daemonState(st)},
		{"device", device, ""},
		{"games", gamesValue(st), ""},
		{"peers", peersValue(st), peersState(st)},
		{"relay", relayValue(st), relayState(st)},
	}
	if st.Conflicts > 0 {
		rows = append(rows, row{"conflicts",
			warnText(fmt.Sprintf("%d save(s)", st.Conflicts)),
			warnText(sym("● needs a decision", "! needs a decision"))})
	}

	// Width the value column to its widest entry so states align.
	valueWidth := 0
	for _, r := range rows {
		if r.state != "" && displayWidth(r.value) > valueWidth {
			valueWidth = displayWidth(r.value)
		}
	}

	fmt.Println()
	markWidth := displayWidth(osMark[0])
	for i, r := range rows {
		mark := strings.Repeat(" ", markWidth)
		if i < len(osMark) {
			mark = accent(osMark[i])
		}
		line := "  " + mark + "   "
		switch {
		case r.label == "" && r.value == "":
			line = strings.TrimRight(line, " ")
		case r.label == "":
			line += r.value
		case r.state == "":
			line += faint(padRight(r.label, 9)) + r.value
		default:
			line += faint(padRight(r.label, 9)) + padRight(r.value, valueWidth+3) + r.state
		}
		fmt.Println(strings.TrimRight(line, " "))
	}

	printQuickCommands(st)
	return 0
}

func daemonValue(st overviewState) string {
	if !st.DaemonUp {
		return faint("—")
	}
	return accent(strings.TrimPrefix(st.DaemonAddr, "http://"))
}

func daemonState(st overviewState) string {
	if !st.DaemonUp {
		return statusDot(false, "not running")
	}
	return statusDot(true, "running")
}

func gamesValue(st overviewState) string {
	if st.Games == 0 {
		return faint("none tracked yet")
	}
	return fmt.Sprintf("%d tracked", st.Games)
}

func peersValue(st overviewState) string {
	if st.PeersTotal == 0 {
		return faint("—")
	}
	return fmt.Sprintf("%d paired", st.PeersTotal)
}

func peersState(st overviewState) string {
	switch {
	case st.PeersTotal == 0:
		return statusDot(false, "none paired")
	case st.Peers == 0:
		return statusDot(false, "none online")
	default:
		return statusDot(true, fmt.Sprintf("%d online", st.Peers))
	}
}

func relayValue(st overviewState) string {
	if st.RelayRoom == "" {
		return faint("—")
	}
	return accent(st.RelayRoom)
}

func relayState(st overviewState) string {
	if st.RelayRoom == "" {
		return statusDot(false, "not joined")
	}
	return statusDot(true, "joined")
}

// printQuickCommands shows the handful of commands that make sense given the
// current state, rather than the full reference.
func printQuickCommands(st overviewState) {
	group := func(title string, lines [][2]string) {
		fmt.Println()
		fmt.Printf("  %s\n", heading(title))
		for _, l := range lines {
			fmt.Printf("    %s %s\n", padRight(accent(l[0]), 28), faint(l[1]))
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
	fmt.Printf("    %s %s\n", padRight(accent("opensave --help"), 28), faint("every command"))
	fmt.Println()
}
