package p2p

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/p2p/pairing"
	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/version"
)

// handleMessage dispatches one relay message, mirroring
// wan-client.js#handleRelayMessage.
func (w *WanClient) handleMessage(ctx context.Context, msg RelayMessage) {
	localID := w.localPeerID()
	if msg.To != "" && msg.To != localID {
		return
	}

	// Presence tracking for any message carrying a sender.
	if msg.From != "" && msg.From != localID {
		w.trackPresence(msg)
	}

	switch msg.Type {
	case "hello":
		if msg.From == localID {
			return
		}
		w.recordDiscovered(msg)

		// They claim we're paired but we aren't: notify unpair (unless a
		// handshake is in flight).
		_, pairedErr := w.engine.Store.GetPeer(msg.From)
		if pairedErr != nil && contains(msg.PairedPeers, localID) && !w.engine.Pairing.HasIncoming(msg.From) {
			w.engine.Log("warn", fmt.Sprintf("WAN peer %s thinks we're paired but we unpaired — notifying", msg.From))
			w.send(RelayMessage{Type: "unpair-notify", To: msg.From, From: localID})
		}

		// Reply so they discover us immediately.
		settings, err := w.engine.Store.GetSettings()
		if err == nil {
			w.send(RelayMessage{
				Type: "hello-reply", To: msg.From, From: localID,
				DeviceName: settings.DeviceName, DeviceType: settings.DeviceType, Port: settings.Port,
				Games:      w.gamesStateJSON(),
				AppVersion: version.Version, BuildTimeMs: version.BuildTimeMs(),
			})
		}

	case "hello-reply", "ping":
		if msg.From != localID {
			w.recordDiscovered(msg)
		}

	case "unpair-notify":
		if msg.From == localID {
			return
		}
		if _, err := w.engine.Store.GetPeer(msg.From); err == nil {
			w.engine.Log("warn", fmt.Sprintf("received unpair-notify from WAN peer %s — unpairing", msg.From))
			_ = w.engine.Store.UnpairPeer(msg.From)
			w.engine.notifyPeerUpdate()
		}

	case "untrack-notify":
		if msg.From == localID || msg.GameID == "" {
			return
		}
		if _, err := w.engine.Store.GetPeer(msg.From); err == nil {
			w.engine.applyPeerUntrack(msg.GameID)
		}

	case "retrack-notify":
		if msg.From == localID || msg.GameID == "" {
			return
		}
		if _, err := w.engine.Store.GetPeer(msg.From); err == nil {
			w.engine.applyPeerRetrack(msg.GameID)
		}

	case "sync-event":
		var ev syncengine.ProgressEvent
		_ = json.Unmarshal(msg.Data, &ev)
		switch msg.EventType {
		case "sync-start":
			if w.engine.Sync.Progress.OnSyncStart != nil {
				w.engine.Sync.Progress.OnSyncStart(msg.GameID, ev)
			}
		case "sync-progress":
			if w.engine.Sync.Progress.OnSyncProgress != nil {
				w.engine.Sync.Progress.OnSyncProgress(msg.GameID, ev)
			}
		case "sync-complete":
			if w.engine.Sync.Progress.OnSyncComplete != nil {
				w.engine.Sync.Progress.OnSyncComplete(msg.GameID, ev)
			}
			// Peer finished pulling from us over the relay: refresh the
			// shared lineage so pushed files start counting as synced.
			if peer, err := w.engine.Store.GetPeer(msg.From); err == nil {
				sp := syncengine.Peer{ID: peer.ID, Name: peer.Name, Address: "relay", Port: peer.Port, IsWan: true}
				go func() {
					refreshCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					w.engine.Sync.RefreshLineage(refreshCtx, msg.GameID, sp)
				}()
			}
		case "in-sync":
			// Peer verified both sides match: confirm on our side (hash
			// re-check) before recording lineage + last-synced.
			var payload struct {
				ManifestHash string `json:"manifestHash"`
			}
			_ = json.Unmarshal(msg.Data, &payload)
			claimedHash := payload.ManifestHash
			if peer, err := w.engine.Store.GetPeer(msg.From); err == nil {
				sp := syncengine.Peer{ID: peer.ID, Name: peer.Name, Address: "relay", Port: peer.Port, IsWan: true}
				go func() {
					refreshCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					w.engine.Sync.ConfirmInSync(refreshCtx, msg.GameID, sp, claimedHash)
				}()
			}
		case "sync-error":
			if w.engine.Sync.Progress.OnSyncError != nil {
				w.engine.Sync.Progress.OnSyncError(msg.GameID, ev)
			}
		}

	case "request":
		go w.serveRequest(ctx, msg)

	case "response":
		w.mu.Lock()
		ch, ok := w.pending[msg.MsgID]
		w.mu.Unlock()
		if ok {
			select {
			case ch <- msg:
			default:
			}
		}
	}
}

// trackPresence flips paired peers online (routing them via relay) and
// refreshes discovered timestamps.
func (w *WanClient) trackPresence(msg RelayMessage) {
	if msg.AppVersion != "" {
		w.engine.recordPeerBuild(msg.From, msg.AppVersion, msg.BuildTimeMs)
	}
	peer, err := w.engine.Store.GetPeer(msg.From)
	if err == nil {
		wasOffline := peer.Status != "online"
		changed := wasOffline || peer.Address != "relay"
		peer.Status = "online"
		peer.Address = "relay"
		peer.LastSeenMs = time.Now().UnixMilli()
		if msg.Port > 0 {
			peer.Port = msg.Port
		}
		_ = w.engine.Store.UpsertPeer(peer)
		if wasOffline {
			w.engine.Log("info", fmt.Sprintf("peer %q came online via WAN; auto-syncing", peer.Name))
			go w.engine.SyncAllGames(context.Background())
		}
		if changed {
			w.engine.notifyPeerUpdate()
		}
	}

	w.mu.Lock()
	if p, ok := w.discovered[msg.From]; ok {
		p.LastSeen = time.Now().UnixMilli()
		w.discovered[msg.From] = p
	}
	w.mu.Unlock()
}

func (w *WanClient) recordDiscovered(msg RelayMessage) {
	if msg.DeviceName == "" {
		return
	}
	w.mu.Lock()
	_, existed := w.discovered[msg.From]
	w.discovered[msg.From] = WanPeer{
		ID: msg.From, DeviceName: msg.DeviceName,
		DeviceType: orDefault(msg.DeviceType, "desktop"),
		Address:    "relay", Port: msg.Port, IsWan: true,
		LastSeen: time.Now().UnixMilli(),
	}
	w.mu.Unlock()
	if !existed {
		w.engine.notifyPeerUpdate()
	}
}

// serveRequest answers an HTTP-shaped RPC from a WAN peer — the relay-side
// equivalent of the /api/p2p/* routes, with the same pairing guard.
func (w *WanClient) serveRequest(ctx context.Context, msg RelayMessage) {
	status, data := w.routeRequest(ctx, msg)
	raw, err := json.Marshal(data)
	if err != nil {
		status = 500
		raw = []byte(`{"error":"response serialization failed"}`)
	}
	w.send(RelayMessage{
		Type: "response", To: msg.From, From: w.localPeerID(),
		MsgID: msg.MsgID, Status: status, Data: raw,
	})
}

func (w *WanClient) routeRequest(ctx context.Context, msg RelayMessage) (int, any) {
	route := msg.Route
	from := msg.From
	w.engine.Log("info", fmt.Sprintf("WAN API request: %s %s from %s", msg.Method, route, from))

	requiresPairing := strings.HasPrefix(route, "/manifest/") ||
		strings.HasPrefix(route, "/blocks/") ||
		strings.HasPrefix(route, "/snapshot/") ||
		strings.HasPrefix(route, "/sync/trigger/") ||
		strings.HasPrefix(route, "/delete-file/") ||
		route == "/unpair"

	_, pairedErr := w.engine.Store.GetPeer(from)
	isPaired := pairedErr == nil

	if requiresPairing && !isPaired {
		w.engine.Log("warn", fmt.Sprintf("blocked %s from unpaired WAN peer %s", route, from))
		return 401, map[string]string{"error": "Unauthorized: Requesting peer is not paired."}
	}

	switch {
	case route == "/ping":
		settings, _ := w.engine.Store.GetSettings()
		return 200, map[string]any{"status": "ok", "deviceName": settings.DeviceName, "deviceType": settings.DeviceType}

	case route == "/handshake":
		var body struct {
			PeerID     string `json:"peerId"`
			DeviceName string `json:"deviceName"`
			DeviceType string `json:"deviceType"`
			Port       int    `json:"port"`
		}
		if err := json.Unmarshal(msg.Body, &body); err != nil || body.PeerID == "" {
			return 400, map[string]string{"error": "peerId is required"}
		}
		w.engine.Pairing.RecordIncoming(pairing.IncomingRequest{
			PeerID: body.PeerID, DeviceName: body.DeviceName,
			DeviceType: orDefault(body.DeviceType, "desktop"),
			Address:    "relay", Port: body.Port, IsWan: true,
		})
		w.engine.notifyPeerUpdate()
		return 200, map[string]any{"status": "pending", "message": "Pairing request received via WAN. Waiting for approval."}

	case route == "/approve-confirm":
		var body struct {
			PeerID     string `json:"peerId"`
			DeviceName string `json:"deviceName"`
			DeviceType string `json:"deviceType"`
			Port       int    `json:"port"`
		}
		if err := json.Unmarshal(msg.Body, &body); err != nil || body.PeerID == "" {
			return 400, map[string]string{"error": "peerId is required"}
		}
		if !w.engine.Pairing.ValidateConfirm(body.PeerID, "relay", body.Port, isPaired) {
			w.engine.Log("warn", "blocked unsolicited WAN approve-confirm from "+body.PeerID)
			return 400, map[string]string{"error": "Pairing confirmation rejected: no matching handshake initiated."}
		}
		w.engine.Pairing.TakeIncoming(body.PeerID)
		if err := w.engine.Store.UpsertPeer(store.Peer{
			ID: body.PeerID, Name: body.DeviceName, DeviceType: orDefault(body.DeviceType, "desktop"),
			Address: "relay", Port: body.Port, Status: "online", LastSeenMs: time.Now().UnixMilli(),
		}); err != nil {
			return 500, map[string]string{"error": err.Error()}
		}
		w.engine.notifyPeerUpdate()
		return 200, map[string]any{"success": true, "message": "Pairing confirmed."}

	case route == "/unpair":
		var body struct {
			PeerID string `json:"peerId"`
		}
		_ = json.Unmarshal(msg.Body, &body)
		_ = w.engine.Store.UnpairPeer(body.PeerID)
		w.engine.notifyPeerUpdate()
		return 200, map[string]any{"success": true, "message": "Unpaired successfully."}

	case strings.HasPrefix(route, "/manifest/"):
		return w.serveManifest(route)

	case strings.HasPrefix(route, "/blocks/"):
		return w.serveBlocks(route, msg.Body)

	case strings.HasPrefix(route, "/delete-file/"):
		return w.serveDeleteFile(route, msg.Body)

	case strings.HasPrefix(route, "/snapshot/"):
		return w.serveSnapshotDownload(route)

	case strings.HasPrefix(route, "/app-binary"):
		if !isPaired {
			return 401, map[string]string{"error": "Unauthorized: Requesting peer is not paired."}
		}
		return serveAppBinaryChunk(route)

	case strings.HasPrefix(route, "/sync/trigger/"):
		gameID := route[strings.LastIndex(route, "/")+1:]
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if _, err := w.engine.SyncGame(syncCtx, gameID); err != nil {
				w.engine.Log("warn", fmt.Sprintf("WAN-triggered sync %s: %v", gameID, err))
			}
		}()
		return 200, map[string]any{"success": true, "message": "Sync triggered."}

	default:
		return 404, map[string]string{"error": "Endpoint not supported over WAN."}
	}
}

func (w *WanClient) serveManifest(route string) (int, any) {
	u, err := url.Parse(route)
	if err != nil {
		return 400, map[string]string{"error": "bad route"}
	}
	gameID := u.Path[strings.LastIndex(u.Path, "/")+1:]

	// Same auto-track + cover-backfill behavior as the LAN route — relay
	// peers were previously auto-tracked without cover art, which is why
	// covers didn't propagate between WAN-paired devices.
	game, err := w.engine.ensureManifestGame(gameID, manifestQueryFromURL(u.Query()))
	if err != nil {
		return 404, map[string]string{"error": err.Error()}
	}

	manifest, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return 500, map[string]string{"error": err.Error()}
	}
	resp := map[string]any{
		"gameId": gameID, "activeBranch": game.ActiveBranch, "manifest": manifest,
	}
	if latest, err := w.engine.Snapshots.LatestSnapshot(gameID, ""); err == nil {
		resp["latestSnapshot"] = syncengine.SnapshotInfo{ID: latest.ID, Timestamp: latest.Timestamp, Comment: latest.Comment}
	} else {
		resp["latestSnapshot"] = nil
	}
	return 200, resp
}

func (w *WanClient) serveBlocks(route string, rawBody json.RawMessage) (int, any) {
	gameID := route[strings.LastIndex(route, "/")+1:]
	var body struct {
		RelPath      string `json:"relPath"`
		BlockIndices []int  `json:"blockIndices"`
		BlockSize    int    `json:"blockSize"`
	}
	if err := json.Unmarshal(rawBody, &body); err != nil || body.RelPath == "" {
		return 400, map[string]string{"error": "relPath is required"}
	}

	game, err := w.engine.Store.GetGame(gameID)
	if err != nil {
		return 404, map[string]string{"error": "Game not found."}
	}
	if !delta.IsSafePath(game.SavePath, body.RelPath) {
		return 403, map[string]string{"error": "Access denied: path traversal attempt detected."}
	}

	fullPath := filepath.Join(game.SavePath, filepath.FromSlash(body.RelPath))
	if isFile, _ := delta.ResolveLocalSaveFilePath(game.SavePath); isFile {
		fullPath = game.SavePath
	}
	blocks, err := delta.ReadBlocks(fullPath, body.BlockIndices, body.BlockSize)
	if err != nil {
		return 500, map[string]string{"error": err.Error()}
	}
	out := make([]syncengine.BlockData, len(blocks))
	for i, b := range blocks {
		out[i] = syncengine.BlockData{Index: b.Index, Data: b.Data, Length: len(b.Data)}
	}
	return 200, map[string]any{"relPath": body.RelPath, "blocks": out}
}

func (w *WanClient) serveDeleteFile(route string, rawBody json.RawMessage) (int, any) {
	gameID := route[strings.LastIndex(route, "/")+1:]
	var body struct {
		RelPath string `json:"relPath"`
	}
	if err := json.Unmarshal(rawBody, &body); err != nil || body.RelPath == "" {
		return 400, map[string]string{"error": "relPath is required."}
	}
	game, err := w.engine.Store.GetGame(gameID)
	if err != nil {
		return 404, map[string]string{"error": "Game not found."}
	}
	if !delta.IsSafePath(game.SavePath, body.RelPath) {
		return 403, map[string]string{"error": "invalid path"}
	}
	full := filepath.Join(game.SavePath, filepath.FromSlash(body.RelPath))
	_ = os.Chmod(full, 0o666)
	_ = os.Remove(full)
	return 200, map[string]any{"success": true}
}

func (w *WanClient) serveSnapshotDownload(route string) (int, any) {
	parts := strings.Split(strings.Trim(route, "/"), "/")
	if len(parts) < 3 {
		return 400, map[string]string{"error": "bad route"}
	}
	snapshotID := parts[len(parts)-1]

	snap, err := w.engine.Store.GetSnapshot(snapshotID)
	if err != nil {
		return 404, map[string]string{"error": "Snapshot ZIP file not found."}
	}
	raw, err := os.ReadFile(snap.ZipPath)
	if err != nil {
		return 404, map[string]string{"error": "Snapshot ZIP file not found."}
	}
	return 200, map[string]any{
		"snapshotId": snapshotID,
		"base64Data": base64.StdEncoding.EncodeToString(raw),
		"fileName":   snapshotID + ".zip",
	}
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
