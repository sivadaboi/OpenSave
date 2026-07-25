package cliapp

import (
	"encoding/json"
	"fmt"
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
		fmt.Println("Sync triggered for all tracked games.")
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
		fmt.Printf("Sync for %q queued behind the one already running.\n", gameID)
		return 0
	}
	fmt.Printf("Sync triggered for %q.\n", gameID)
	return 0
}

// cmdPeers lists paired devices and their status.
func cmdPeers(args []string) int {
	asJSON, _ := jsonFlag(args)

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

	if len(payload.Peers) == 0 {
		fmt.Println("No paired devices.")
	} else {
		fmt.Printf("Paired (%d):\n", len(payload.Peers))
		for id, p := range payload.Peers {
			status := p.Status
			if status == "" {
				status = "unknown"
			}
			addr := p.Address
			if addr != "" && p.Port != 0 {
				addr = fmt.Sprintf("%s:%d", p.Address, p.Port)
			}
			fmt.Printf("  %-24s %-9s %s\n", p.Name, status, addr)
			fmt.Printf("  %-24s %s\n", "", id)
		}
	}

	if len(payload.PairingRequests) > 0 {
		fmt.Printf("\nIncoming pairing requests (%d) — approve with `opensave pair approve <id>`:\n",
			len(payload.PairingRequests))
		for _, r := range payload.PairingRequests {
			fmt.Printf("  %-24s %s\n", r.Name, r.PeerID)
		}
	}
	if len(payload.DiscoveredPeers) > 0 {
		fmt.Printf("\nDiscovered on this network (%d) — pair with `opensave pair <address>`:\n",
			len(payload.DiscoveredPeers))
		for _, d := range payload.DiscoveredPeers {
			addr := d.Address
			if addr != "" && d.Port != 0 {
				addr = fmt.Sprintf("%s:%d", d.Address, d.Port)
			}
			fmt.Printf("  %-24s %s\n", d.Name, addr)
		}
	}
	if len(payload.Peers) == 0 && len(payload.DiscoveredPeers) == 0 {
		fmt.Println("\nNothing discovered yet. On the same network, devices find each other")
		fmt.Println("automatically; across the internet use `opensave relay join <code>`.")
	}
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
		Name   string `json:"name"`
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
		fmt.Printf("%d pending request(s):\n\n", len(payload.PairingRequests))
		for _, r := range payload.PairingRequests {
			fmt.Printf("  %-24s %s\n", r.Name, r.PeerID)
		}
		fmt.Println("\nApprove with `opensave pair approve <id>`.")
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
		fmt.Printf("Approved pairing with %s.\n", args[1])
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
		fmt.Printf("Rejected pairing with %s.\n", args[1])
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
		fmt.Printf("Pairing request sent to %s:%d.\n", host, port)
		fmt.Println("Approve it on that device (or run `opensave pair approve <peerId>` there).")
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
	fmt.Printf("Unpaired %s.\n", args[0])
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
		if s.SyncCode == "" {
			fmt.Println("Not in a relay room. Join one with `opensave relay join <code>`.")
			return 0
		}
		fmt.Printf("Relay room: %s\n", s.SyncCode)
		fmt.Printf("Relay server: %s\n", s.RelayURL)
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
		fmt.Printf("Joined relay room %q. Devices in the same room can now find each other.\n", args[1])
		return 0

	case "leave":
		raw, err := daemonRequest("POST", "/api/settings", map[string]any{"syncCode": ""})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		fmt.Println("Left the relay room.")
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
