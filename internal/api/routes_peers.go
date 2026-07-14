package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// peerRoutes: pairing lifecycle + manual sync + conflict resolution.
func (s *Server) peerRoutes(r chi.Router) {
	r.Get("/api/peers", s.handleListPeers)
	r.Post("/api/peers/pair", s.handlePair)
	r.Post("/api/peers/approve", s.handleApprovePairing)
	r.Post("/api/peers/reject", s.handleRejectPairing)
	r.Post("/api/peers/unpair", s.handleUnpairPeer)
	r.Delete("/api/peers/{peerId}", s.handleDeletePeer)
	r.Post("/api/peers/probe", s.handleProbePeer)

	r.Post("/api/games/sync-all", s.handleSyncAll)
	r.Post("/api/games/{gameId}/sync", s.handleSyncGame)
	r.Post("/api/games/{gameId}/resolve-conflict", s.handleResolveConflict)

	r.Get("/api/wan/status", s.handleWanStatus)
	r.Get("/api/relay/health", s.handleRelayHealth)
	r.Get("/api/relay/ips", s.handleRelayIPs)
	r.Post("/api/relay/reconnect", s.handleRelayReconnect)
}

// handleRelayReconnect forces a fresh relay connection attempt. Saving
// settings only reconnects when the code/URL changed, so the UI needs an
// explicit retry for "the relay was down/waking, try again now".
func (s *Server) handleRelayReconnect(w http.ResponseWriter, r *http.Request) {
	s.Daemon.P2P.Wan.Connect()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "wanRoom": s.Daemon.P2P.Wan.Status()})
}

// handleRelayIPs returns this machine's LAN addresses and public IP, plus
// the LAN sync PIN — the info a self-hoster shares with friends.
func (s *Server) handleRelayIPs(w http.ResponseWriter, r *http.Request) {
	lanIPs := []string{}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
					lanIPs = append(lanIPs, ipNet.IP.String())
				}
			}
		}
	}

	// Public IP is best-effort; a timeout just yields "".
	publicIP := ""
	client := http.Client{Timeout: 4 * time.Second}
	if resp, err := client.Get("https://api.ipify.org"); err == nil {
		defer resp.Body.Close()
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 64)); err == nil {
			publicIP = strings.TrimSpace(string(body))
		}
	}

	settings, _ := s.Daemon.Store.GetSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"lanIps":    lanIPs,
		"publicIp":  publicIP,
		"relayPort": settings.RelayPort,
		"syncCode":  settings.SyncCode,
	})
}

func (s *Server) handleWanStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Daemon.P2P.Wan.Status())
}

// handleRelayHealth probes the configured relay's /health endpoint
// (converting ws(s):// to http(s):// like the JS daemon did).
func (s *Server) handleRelayHealth(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Daemon.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpURL := strings.Replace(strings.Replace(settings.RelayURL, "wss://", "https://", 1), "ws://", "http://", 1)

	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(httpURL + "/health")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"reachable": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	var health map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&health)
	writeJSON(w, http.StatusOK, map[string]any{"reachable": resp.StatusCode == http.StatusOK, "health": health})
}

func (s *Server) handleListPeers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.peersPayload())
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
		Port    int    `json:"port"`
		PeerID  string `json:"peerId"` // WAN room member — pair through the relay
	}
	if err := readJSON(r, &body); err != nil || (body.Address == "" && body.PeerID == "") {
		writeError(w, http.StatusBadRequest, "address or peerId is required")
		return
	}

	if body.PeerID != "" && (body.Address == "" || body.Address == "relay") {
		if err := s.Daemon.P2P.InitiatePairWan(r.Context(), body.PeerID); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	}

	if body.Port == 0 {
		body.Port = 8383
	}
	if err := s.Daemon.P2P.InitiatePair(r.Context(), body.Address, body.Port); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
}

func (s *Server) handleApprovePairing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PeerID string `json:"peerId"`
	}
	if err := readJSON(r, &body); err != nil || body.PeerID == "" {
		writeError(w, http.StatusBadRequest, "peerId is required")
		return
	}
	if err := s.Daemon.P2P.ApprovePairing(r.Context(), body.PeerID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.BroadcastPeersUpdate()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRejectPairing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PeerID string `json:"peerId"`
	}
	if err := readJSON(r, &body); err != nil || body.PeerID == "" {
		writeError(w, http.StatusBadRequest, "peerId is required")
		return
	}
	s.Daemon.P2P.RejectPairing(body.PeerID)
	s.BroadcastPeersUpdate()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleUnpairPeer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PeerID string `json:"peerId"`
	}
	if err := readJSON(r, &body); err != nil || body.PeerID == "" {
		writeError(w, http.StatusBadRequest, "peerId is required")
		return
	}
	s.unpair(w, body.PeerID)
}

func (s *Server) handleDeletePeer(w http.ResponseWriter, r *http.Request) {
	s.unpair(w, chi.URLParam(r, "peerId"))
}

func (s *Server) unpair(w http.ResponseWriter, peerID string) {
	if err := s.Daemon.P2P.Unpair(peerID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.BroadcastPeersUpdate()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// handleProbePeer checks whether an address:port hosts a reachable
// OpenSave daemon (used by the "add device by IP" UI flow).
func (s *Server) handleProbePeer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
		Port    int    `json:"port"`
	}
	if err := readJSON(r, &body); err != nil || body.Address == "" {
		writeError(w, http.StatusBadRequest, "address is required")
		return
	}
	if body.Port == 0 {
		body.Port = 8383
	}

	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + body.Address + ":" + itoa(body.Port) + "/api/p2p/ping")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"reachable": false, "error": err.Error()})
		return
	}
	resp.Body.Close()
	writeJSON(w, http.StatusOK, map[string]any{"reachable": resp.StatusCode == http.StatusOK})
}

// handleSyncAll triggers a sync of every tracked game (used by the Steam
// Deck plugin's one-button flow).
func (s *Server) handleSyncAll(w http.ResponseWriter, r *http.Request) {
	games, err := s.Daemon.Store.ListGames()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	results := map[string]any{}
	for _, g := range games {
		if !g.AutoSync {
			results[g.ID] = map[string]string{"status": "skipped", "reason": "autoSync disabled"}
			continue
		}
		res, err := s.Daemon.P2P.SyncGame(r.Context(), g.ID)
		if err != nil {
			results[g.ID] = map[string]string{"status": "error", "error": err.Error()}
			continue
		}
		results[g.ID] = res
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleSyncGame(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	results, err := s.Daemon.P2P.SyncGame(r.Context(), gameID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleResolveConflict(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		PeerID     string `json:"peerId"`
		Resolution string `json:"resolution"`
	}
	if err := readJSON(r, &body); err != nil || body.Resolution == "" {
		writeError(w, http.StatusBadRequest, "peerId and resolution are required")
		return
	}
	switch body.Resolution {
	case "keep-local", "keep-remote", "merge-branch":
	default:
		writeError(w, http.StatusBadRequest, "invalid conflict resolution "+body.Resolution)
		return
	}

	// Applying a resolution can pull the peer's whole save over the relay —
	// minutes of transfer. Run it in the background and report the outcome
	// via the "conflict-resolved" WS broadcast, so the modal's button click
	// returns instantly instead of freezing the UI until the pull finishes.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		branchName, err := s.Daemon.P2P.Sync.ResolveConflict(ctx, gameID, body.PeerID, body.Resolution)

		payload := map[string]any{"gameId": gameID, "resolution": body.Resolution}
		if branchName != "" {
			payload["branchName"] = branchName
		}
		if err != nil {
			payload["error"] = err.Error()
		}
		s.Hub.Broadcast("conflict-resolved", payload)
		s.BroadcastGamesUpdate()
		s.BroadcastPeersUpdate()
	}()

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "status": "applying", "resolution": body.Resolution})
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
