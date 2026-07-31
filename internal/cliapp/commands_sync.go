package cliapp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// These are the commands that make the CLI a real headless client rather than
// a local snapshot utility: without them a server or a Steam Deck in Game Mode
// can track games but never pair with anything or sync.

// cmdSync triggers a sync for one game or all of them.
func cmdSync(args []string) int {
	asJSON, args := jsonFlag(args)

	if len(args) == 0 || args[0] == "--all" {
		raw, err := daemonRequest("POST", "/api/games/sync-all", map[string]any{})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		success("Sync started for all tracked games.")
		return 0
	}

	gameID := args[0]
	raw, err := daemonRequest("POST", "/api/games/"+gameID+"/sync", map[string]any{})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	// A sync that lands while another is running is queued, not an error.
	var res struct {
		Queued bool `json:"queued"`
	}
	if json.Unmarshal(raw, &res) == nil && res.Queued {
		success("Queued %s behind the sync already running.", bold(gameID))
		return 0
	}
	success("Sync started for %s.", bold(gameID))
	return 0
}

// conflictSideLabel says which side a difference is on, naming the peer
// rather than saying "remote" — the user knows their devices by name.
func conflictSideLabel(status, peerName string) string {
	switch status {
	case "only-local":
		return "only here"
	case "only-remote":
		return "only " + peerName
	default:
		return "differs"
	}
}

// conflictSizeNote renders the two sizes when both exist, and one when the
// file is on a single side. Directories carry -1 on both and get nothing:
// "0 B" beside a folder name reads as an empty file.
func conflictSizeNote(d conflictDiffFile) string {
	switch {
	case d.LocalSize >= 0 && d.RemoteSize >= 0:
		return fmt.Sprintf("(%s / %s)", humanBytes(d.LocalSize), humanBytes(d.RemoteSize))
	case d.LocalSize >= 0:
		return "(" + humanBytes(d.LocalSize) + ")"
	case d.RemoteSize >= 0:
		return "(" + humanBytes(d.RemoteSize) + ")"
	default:
		return ""
	}
}

// cmdConflicts lists saves that diverged on two devices and are waiting on a
// decision. Until one is made that game stops syncing, so a headless machine
// needs to see and settle them without a desktop.
func cmdConflicts(args []string) int {
	asJSON, _ := jsonFlag(args)

	conflicts, err := fetchConflicts()
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(conflicts)
	}
	if len(conflicts) == 0 {
		section("Conflicts")
		fmt.Printf("  %s Everything is in sync.\n", symOK())
		fmt.Println()
		return 0
	}

	section(fmt.Sprintf("Conflicts %s %d waiting on a decision", symDot(), len(conflicts)))
	t := newTable("game", "diverged from", "newer side", "files")
	for gameID, c := range conflicts {
		var newer string
		switch {
		case c.LocalStats.LatestMtimeMs > c.RemoteStats.LatestMtimeMs:
			newer = accent("this device")
		case c.RemoteStats.LatestMtimeMs > c.LocalStats.LatestMtimeMs:
			newer = accent(c.Peer.Name)
		default:
			newer = faint("same age")
		}
		files := faint("—")
		if c.DiffTotal > 0 {
			files = fmt.Sprintf("%d", c.DiffTotal)
		}
		t.add(bold(gameID), c.Peer.Name, newer, files)
	}
	t.render()

	// Then what differs, per game. Capped: a conflict over a save with
	// hundreds of files should not bury the resolve hints below it, and the
	// count in the table above is already the honest total.
	const maxShown = 12
	for gameID, c := range conflicts {
		if len(c.DiffFiles) == 0 {
			continue
		}
		fmt.Printf("\n  %s %s\n", symBullet(), bold(gameID))

		// Width from the labels actually being printed: "only <device>" is as
		// long as the device is named, so a fixed column either wastes space
		// or fails to align the moment someone's PC is called something.
		labelWidth := 0
		for i, d := range c.DiffFiles {
			if i == maxShown {
				break
			}
			if n := len(conflictSideLabel(d.Status, c.Peer.Name)); n > labelWidth {
				labelWidth = n
			}
		}

		for i, d := range c.DiffFiles {
			if i == maxShown {
				fmt.Printf("      %s\n", faint(fmt.Sprintf("…and %d more", len(c.DiffFiles)-maxShown)))
				break
			}
			fmt.Printf("      %s  %s %s\n",
				faint(padRight(conflictSideLabel(d.Status, c.Peer.Name), labelWidth)),
				d.Path,
				faint(conflictSizeNote(d)))
		}
	}

	hint(
		"opensave resolve <game> keep-both      keeps both, theirs on a branch (safest)",
		"opensave resolve <game> keep-local     this device's save wins",
		"opensave resolve <game> keep-remote    the other device's save wins",
	)
	fmt.Println()
	return 0
}

// cmdResolve settles one conflict. The peer id is looked up from the conflict
// itself, so the user never has to find and type a node id.
func cmdResolve(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, resolveUsage)
		return 1
	}
	gameID, choice := args[0], args[1]

	// "keep-both" is what the app calls this; merge-branch is the wire name.
	resolution := choice
	if choice == "keep-both" {
		resolution = "merge-branch"
	}
	switch resolution {
	case "keep-local", "keep-remote", "merge-branch":
	default:
		return fail(asJSON, fmt.Errorf("unknown resolution %q\n\n%s", choice, resolveUsage))
	}

	conflicts, err := fetchConflicts()
	if err != nil {
		return fail(asJSON, err)
	}
	c, ok := conflicts[gameID]
	if !ok {
		return fail(asJSON, fmt.Errorf("no active conflict for %q — run `opensave conflicts`", gameID))
	}

	if _, err := daemonRequest("POST", "/api/games/"+gameID+"/resolve-conflict", map[string]any{
		"peerId":     c.Peer.ID,
		"resolution": resolution,
	}); err != nil {
		return fail(asJSON, err)
	}

	if asJSON {
		return emitJSON(map[string]any{"game": gameID, "resolution": resolution, "applying": true})
	}
	// Applying can pull the peer's whole save, so the daemon does it in the
	// background; the request only confirms it was accepted.
	success("Resolving %s (%s).", bold(gameID), accent(choice))
	note("This runs in the background — a large save can take a while.")
	hint("opensave conflicts")
	return 0
}

const resolveUsage = `usage: opensave resolve <gameId> keep-both|keep-local|keep-remote

  keep-both     Keep both saves; the peer's lands on a separate branch (safest)
  keep-local    This device's save wins
  keep-remote   The other device's save wins`

// conflictInfo mirrors the conflict shape /api/status reports.
type conflictInfo struct {
	Peer struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"peer"`
	LocalStats struct {
		LatestMtimeMs int64 `json:"latestMtimeMs"`
	} `json:"localStats"`
	RemoteStats struct {
		LatestMtimeMs int64 `json:"latestMtimeMs"`
	} `json:"remoteStats"`
	DiffTotal int `json:"diffTotal"`
	// DiffFiles is what actually differs. The count alone tells you a
	// decision is needed but nothing about which way to decide, which is the
	// question in front of the user — the app has shown this list since the
	// conflict dialog was rebuilt; the CLI was decoding the count beside it
	// and dropping the rest of the payload on the floor.
	DiffFiles []conflictDiffFile `json:"diffFiles"`
}

type conflictDiffFile struct {
	Path string `json:"path"`
	// changed | only-local | only-remote
	Status     string `json:"status"`
	LocalSize  int64  `json:"localSize"`
	RemoteSize int64  `json:"remoteSize"`
}

func fetchConflicts() (map[string]conflictInfo, error) {
	raw, err := daemonRequest("GET", "/api/status", nil)
	if err != nil {
		return nil, err
	}
	var st struct {
		Conflicts map[string]conflictInfo `json:"conflicts"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	if st.Conflicts == nil {
		st.Conflicts = map[string]conflictInfo{}
	}
	return st.Conflicts, nil
}

// cmdPeers lists paired devices and their status.
// cmdPeerGames lists what a paired device is tracking.
//
// `opensave link` already accepts a peer's game id — LinkGames records the
// alias and leaves the local library alone when the id isn't one of ours — so
// a cross-device link was possible from here, but only if you already knew an
// id the CLI had no way to show you. The desktop picker listed them; this is
// the same list.
func cmdPeerGames(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr,
			"usage: opensave peers games <peerId>\n"+
				"  Lists what that device is tracking, so you can link one of its\n"+
				"  entries to a game here with `opensave link`.\n"+
				"  Device ids come from `opensave peers`.")
		return 1
	}
	peerID := args[0]

	raw, err := daemonRequest("GET", "/api/peers/"+url.PathEscape(peerID)+"/games", nil)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}

	var games []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SavePath string `json:"savePath"`
		AppID    string `json:"appId"`
	}
	if json.Unmarshal(raw, &games) != nil {
		return emitRawJSON(raw)
	}
	if len(games) == 0 {
		section(peerID)
		note("That device isn't tracking anything.")
		fmt.Println()
		return 0
	}

	section(fmt.Sprintf("%s %s %d game(s)", peerID, symDot(), len(games)))
	t := newTable("id", "name", "app id", "path")
	for _, g := range games {
		appID := faint("—")
		if g.AppID != "" {
			appID = g.AppID
		}
		t.add(accent(g.ID), bold(g.Name), appID, faint(g.SavePath))
	}
	t.render()
	hint("opensave link <localGameId> <theirGameId>     treat them as the same game")
	fmt.Println()
	return 0
}

func cmdPeers(args []string) int {
	asJSON, rest := jsonFlag(args)

	// `peers games <id>` asks a device what it holds; bare `peers` lists the
	// devices themselves.
	if len(rest) > 0 && rest[0] == "games" {
		return cmdPeerGames(args[1:])
	}

	raw, err := daemonRequest("GET", "/api/peers", nil)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}

	payload, err := decodePeersPayload(raw)
	if err != nil {
		return emitRawJSON(raw) // shape changed; show what we got
	}

	if len(payload.Peers) > 0 {
		section("Paired devices")
		t := newTable("device", "status", "address", "id")
		for id, p := range payload.Peers {
			var status string
			switch p.Status {
			case "online":
				status = okText(sym("● online", "online"))
			case "offline", "":
				status = faint(sym("● offline", "offline"))
			default:
				status = warnText(p.Status)
			}
			addr := p.Address
			if addr != "" && p.Port != 0 {
				addr = fmt.Sprintf("%s:%d", p.Address, p.Port)
			}
			t.add(bold(p.Name), status, faint(addr), faint(id))
		}
		t.render()
	}

	if len(payload.PairingRequests) > 0 {
		section("Waiting for your approval")
		t := newTable("device", "from", "id")
		for _, r := range payload.PairingRequests {
			name := r.DeviceName
			if name == "" {
				name = "(unnamed device)"
			}
			origin := fmt.Sprintf("%s:%d", r.Address, r.Port)
			if r.IsWan {
				origin = "over the relay"
			}
			t.add(bold(name), faint(origin), faint(r.PeerID))
		}
		t.render()
		hint("opensave pair approve <id>")
	}

	if len(payload.DiscoveredPeers) > 0 {
		section("Found on this network")
		t := newTable("device", "address")
		for _, d := range payload.DiscoveredPeers {
			addr := d.Address
			if addr != "" && d.Port != 0 {
				addr = fmt.Sprintf("%s:%d", d.Address, d.Port)
			}
			t.add(bold(d.Name), faint(addr))
		}
		t.render()
		hint("opensave pair <address>")
	}

	if len(payload.Peers) == 0 && len(payload.DiscoveredPeers) == 0 && len(payload.PairingRequests) == 0 {
		section("Devices")
		note("Nothing paired or discovered yet.")
		hint(
			"opensave pair <address>        same network",
			"opensave relay join <code>     different networks",
		)
	}
	fmt.Println()
	return 0
}

// peersPayload is what /api/peers returns: the whole dashboard peer state,
// not just paired devices.
type peersPayload struct {
	Peers map[string]struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Address string `json:"address"`
		Port    int    `json:"port"`
	} `json:"peers"`
	DiscoveredPeers []struct {
		Name    string `json:"name"`
		Address string `json:"address"`
		Port    int    `json:"port"`
	} `json:"discoveredPeers"`
	PairingRequests []struct {
		PeerID string `json:"peerId"`
		// The daemon sends this as "deviceName"; decoding it as "name" left
		// the column blank on every request, so approving one meant trusting
		// a bare node id with no way to tell which machine was asking.
		DeviceName string `json:"deviceName"`
		Address    string `json:"address"`
		Port       int    `json:"port"`
		IsWan      bool   `json:"isWan"`
	} `json:"pairingRequests"`
}

func decodePeersPayload(raw []byte) (peersPayload, error) {
	var p peersPayload
	err := json.Unmarshal(raw, &p)
	return p, err
}

// cmdPair handles the pairing lifecycle: request, inspect and approve.
// Pairing is mutual by design, so a headless device needs both halves —
// asking to pair, and approving someone else's request.
func cmdPair(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, pairUsage)
		return 1
	}

	switch args[0] {
	case "requests":
		raw, err := daemonRequest("GET", "/api/peers", nil)
		if err != nil {
			return fail(asJSON, err)
		}
		payload, err := decodePeersPayload(raw)
		if err != nil {
			return emitRawJSON(raw)
		}
		if asJSON {
			return emitJSON(payload.PairingRequests)
		}
		if len(payload.PairingRequests) == 0 {
			fmt.Println("No pending pairing requests.")
			return 0
		}
		section(fmt.Sprintf("Pairing requests %s %d waiting", symDot(), len(payload.PairingRequests)))
		t := newTable("DEVICE", "FROM", "ID")
		for _, r := range payload.PairingRequests {
			name := r.DeviceName
			if name == "" {
				name = "(unnamed device)"
			}
			origin := fmt.Sprintf("%s:%d", r.Address, r.Port)
			if r.IsWan {
				origin = "over the relay"
			}
			t.add(bold(name), faint(origin), faint(r.PeerID))
		}
		t.render()
		hint("opensave pair approve <id>", "opensave pair reject <id>")
		fmt.Println()
		return 0

	case "approve":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave pair approve <peerId>")
			return 1
		}
		raw, err := daemonRequest("POST", "/api/peers/approve", map[string]any{"peerId": args[1]})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		success("Paired with %s.", bold(args[1]))
		return 0

	case "reject":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave pair reject <peerId>")
			return 1
		}
		raw, err := daemonRequest("POST", "/api/peers/reject", map[string]any{"peerId": args[1]})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		success("Rejected %s.", bold(args[1]))
		return 0

	default:
		// `opensave pair <host[:port]>` — ask that device to pair.
		host := args[0]
		port := 8383
		if h, p, ok := strings.Cut(host, ":"); ok {
			host = h
			fmt.Sscanf(p, "%d", &port)
		}
		raw, err := daemonRequest("POST", "/api/peers/pair", map[string]any{
			"address": host,
			"port":    port,
		})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		success("Pairing request sent to %s.", bold(fmt.Sprintf("%s:%d", host, port)))
		note("Pairing is mutual — approve it on that device to finish.")
		hint("opensave pair requests     (on the other device)")
		return 0
	}
}

const pairUsage = `usage:
  opensave pair <host[:port]>     Ask a device on your LAN to pair
  opensave pair requests          Show incoming pairing requests
  opensave pair approve <peerId>  Approve an incoming request
  opensave pair reject <peerId>   Reject an incoming request`

// cmdUnpair drops a paired device.
func cmdUnpair(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave unpair <peerId>")
		return 1
	}
	raw, err := daemonRequest("POST", "/api/peers/unpair", map[string]any{"peerId": args[0]})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	success("Unpaired %s.", bold(args[0]))
	return 0
}

// cmdRelay manages internet sync, which on a headless box is the only way to
// reach devices that aren't on the same LAN.
func cmdRelay(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, relayUsage)
		return 1
	}

	switch args[0] {
	case "status":
		raw, err := daemonRequest("GET", "/api/settings", nil)
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		var s struct {
			RelayURL string `json:"relayUrl"`
			SyncCode string `json:"syncCode"`
		}
		if json.Unmarshal(raw, &s) != nil {
			return emitRawJSON(raw)
		}
		section("Internet sync")
		if s.SyncCode == "" {
			field("room", faint("not joined"))
			field("relay", faint(s.RelayURL))
			hint("opensave relay join <code>     same code on every device")
			fmt.Println()
			return 0
		}
		field("room", accent(s.SyncCode))
		field("relay", faint(s.RelayURL))
		fmt.Println()
		return 0

	case "join":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave relay join <code>")
			return 1
		}
		raw, err := daemonRequest("POST", "/api/settings", map[string]any{"syncCode": args[1]})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		success("Joined relay room %s.", accent(args[1]))
		note("Use the same code on your other devices so they find each other.")
		return 0

	case "leave":
		raw, err := daemonRequest("POST", "/api/settings", map[string]any{"syncCode": ""})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		success("Left the relay room.")
		return 0

	default:
		fmt.Fprintln(os.Stderr, relayUsage)
		return 1
	}
}

const relayUsage = `usage:
  opensave relay status        Show the current relay room
  opensave relay join <code>   Join a relay room (same code on every device)
  opensave relay leave         Leave the current room`
