// Package p2p wires peer discovery, pairing, the sync engine, and the
// /api/p2p/* peer protocol routes into one engine — the Go counterpart of
// src/daemon/p2p/index.js.
package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/p2p/discovery"
	"github.com/opensave/opensave/internal/p2p/pairing"
	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

// resyncRetryInterval is how often the failsafe re-attempts games whose
// sync was interrupted (e.g. the network dropped mid-transfer).
const resyncRetryInterval = 20 * time.Second

// reconcileEveryNTicks controls how often (in resyncRetryInterval ticks) a
// full reconcile runs — 3 × 20s = every 60s.
const reconcileEveryNTicks = 3

// GameState is the lightweight per-game summary exchanged in pings/hellos
// so peers can see what each other has without a full manifest fetch.
type GameState struct {
	LatestSnapshotID   string `json:"latestSnapshotId"`
	LatestSnapshotTime int64  `json:"latestSnapshotTime"`
	ActiveBranch       string `json:"activeBranch"`
	ManifestHash       string `json:"manifestHash"`
}

// Engine owns all P2P state for one daemon.
type Engine struct {
	Store     *store.Store
	Snapshots *snapshot.Manager
	Sync      *syncengine.Engine
	Pairing   *pairing.Manager
	Discovery *discovery.Manager
	Wan       *WanClient
	RelayHost *RelayHost
	Log       func(level, msg string)

	// OnPeerUpdate fires whenever peer/pairing state changes (dashboard
	// broadcast hook). May be nil.
	OnPeerUpdate func()

	// OnGamesUpdate fires when the game list changes on this device from a
	// peer action (e.g. auto-tracking a synced game, backfilling its cover).
	// May be nil.
	OnGamesUpdate func()

	// OnUntrackRequest / OnRetrackRequest fire when a paired peer tells us it
	// untracked or re-tracked a game, so this device mirrors the change
	// (remove + tombstone / clear tombstone) without re-notifying. Wired by
	// the daemon. May be nil.
	OnUntrackRequest func(gameID string)
	OnRetrackRequest func(gameID string)

	// Failsafe: games whose last sync was interrupted (network error mid-
	// transfer) are queued here and retried automatically, no prompt, until
	// they complete.
	pendingMu     sync.Mutex
	pendingResync map[string]bool
	stopRetry     chan struct{}

	// Short-lived manifest-hash cache for ping/hello responses. Every
	// incoming ping used to re-hash every tracked save from scratch —
	// constant disk/CPU churn with the 20s retry loop pinging both ways.
	hashCacheMu sync.Mutex
	hashCache   map[string]cachedManifestHash

	// Live per-peer app build info (version + build time) learned from
	// pings/hellos, powering the "update from this device" flow.
	buildMu    sync.Mutex
	peerBuilds map[string]PeerBuild
}

type cachedManifestHash struct {
	hash string
	at   time.Time
}

// manifestHashTTL bounds how stale a ping-response manifest hash can be.
const manifestHashTTL = 20 * time.Second

// StartDiscovery begins UDP LAN presence broadcasting. Paired peers seen
// on the LAN flip online (triggering auto-sync when they were offline);
// unseen peers age out to offline.
func (e *Engine) StartDiscovery() error {
	identity := func() discovery.Ping {
		settings, err := e.Store.GetSettings()
		if err != nil {
			return discovery.Ping{}
		}
		return discovery.Ping{
			NodeID:     settings.NodeID,
			DeviceName: settings.DeviceName,
			DeviceType: settings.DeviceType,
			Port:       settings.Port,
		}
	}

	e.Discovery = discovery.New(identity, discovery.Callbacks{
		OnPeerSeen: func(d discovery.Discovered, isNew bool) {
			peer, err := e.Store.GetPeer(d.ID)
			if err == nil {
				wasOffline := peer.Status != "online"
				peer.Address = d.Address
				peer.Port = d.Port
				peer.DeviceType = d.DeviceType
				peer.Status = "online"
				peer.LastSeenMs = d.LastSeen
				_ = e.Store.UpsertPeer(peer)
				if wasOffline {
					e.Log("info", fmt.Sprintf("paired peer %q appeared on LAN; auto-syncing", peer.Name))
					go e.SyncAllGames(context.Background())
					e.notifyPeerUpdate()
				}
			} else if isNew {
				e.notifyPeerUpdate()
			}
		},
		OnExpired: func(d discovery.Discovered) {
			peer, err := e.Store.GetPeer(d.ID)
			if err == nil && peer.Status == "online" && peer.Address != "relay" {
				peer.Status = "offline"
				_ = e.Store.UpsertPeer(peer)
			}
			e.notifyPeerUpdate()
		},
	})
	return e.Discovery.Start()
}

// Stop shuts down discovery, the WAN client, and any hosted relay.
func (e *Engine) Stop() {
	e.pendingMu.Lock()
	if e.stopRetry != nil {
		close(e.stopRetry)
		e.stopRetry = nil
	}
	e.pendingMu.Unlock()
	if e.Discovery != nil {
		e.Discovery.Stop()
	}
	if e.Wan != nil {
		e.Wan.Disconnect()
	}
	if e.RelayHost != nil {
		e.RelayHost.Stop()
	}
}

// ApplyRelayHosting starts/stops the in-process relay to match settings.
func (e *Engine) ApplyRelayHosting(enabled bool, port int) {
	if e.RelayHost != nil {
		e.RelayHost.Apply(enabled, port)
	}
}

// New assembles a P2P engine with LAN + WAN transports routed per peer.
func New(s *store.Store, snaps *snapshot.Manager, logf func(level, msg string)) *Engine {
	e := &Engine{
		Store:     s,
		Snapshots: snaps,
		Pairing:   pairing.New(),
		Log:       logf,
	}
	e.Wan = newWanClient(e)
	e.RelayHost = NewRelayHost(logf)
	e.Sync = syncengine.New(s, snaps, &routingTransport{
		lan: &lanTransport{},
		wan: &wanTransport{wan: e.Wan},
	})
	e.Sync.Log = logf
	return e
}

// LocalGamesState builds the per-game summary for ping/hello responses.
func (e *Engine) LocalGamesState() map[string]GameState {
	out := map[string]GameState{}
	games, err := e.Store.ListGames()
	if err != nil {
		return out
	}
	for _, g := range games {
		state := GameState{ActiveBranch: g.ActiveBranch}
		if latest, err := e.Snapshots.LatestSnapshot(g.ID, ""); err == nil {
			state.LatestSnapshotID = latest.ID
			if t, err := time.Parse("2006-01-02T15:04:05.000Z", latest.Timestamp); err == nil {
				state.LatestSnapshotTime = t.UnixMilli()
			}
		}
		state.ManifestHash = e.manifestHashCached(g.ID, g.SavePath)
		out[g.ID] = state
	}
	return out
}

// manifestHashCached returns the game's manifest hash, re-hashing at most
// once per manifestHashTTL per game.
func (e *Engine) manifestHashCached(gameID, savePath string) string {
	e.hashCacheMu.Lock()
	if c, ok := e.hashCache[gameID]; ok && time.Since(c.at) < manifestHashTTL {
		e.hashCacheMu.Unlock()
		return c.hash
	}
	e.hashCacheMu.Unlock()

	hash := ""
	if m, err := delta.BuildManifest(savePath); err == nil {
		hash = m.ManifestHash()
	}

	e.hashCacheMu.Lock()
	if e.hashCache == nil {
		e.hashCache = map[string]cachedManifestHash{}
	}
	e.hashCache[gameID] = cachedManifestHash{hash: hash, at: time.Now()}
	e.hashCacheMu.Unlock()
	return hash
}

// OnlinePeers returns paired peers currently marked online, as sync-engine
// peer descriptors.
func (e *Engine) OnlinePeers() []syncengine.Peer {
	peers, err := e.Store.ListPeers()
	if err != nil {
		return nil
	}
	var online []syncengine.Peer
	for _, p := range peers {
		if p.Status == "online" {
			online = append(online, syncengine.Peer{
				ID: p.ID, Name: p.Name, Address: p.Address, Port: p.Port,
				IsWan: p.Address == "relay",
			})
		}
	}
	return online
}

// PingPairedPeers probes every paired LAN peer and updates their
// online/offline status.
func (e *Engine) PingPairedPeers(ctx context.Context) {
	peers, err := e.Store.ListPeers()
	if err != nil {
		return
	}
	settings, err := e.Store.GetSettings()
	if err != nil {
		return
	}

	changed := false
	for _, p := range peers {
		if p.Address == "relay" {
			continue // WAN presence is heartbeat-driven (Phase 3)
		}
		info, ok := pingPeer(ctx, p, settings.NodeID)
		if ok && info.AppVersion != "" {
			e.recordPeerBuild(p.ID, info.AppVersion, info.BuildTimeMs)
		}
		newStatus := "offline"
		if ok {
			newStatus = "online"
		}
		if p.Status != newStatus {
			p.Status = newStatus
			p.LastSeenMs = time.Now().UnixMilli()
			_ = e.Store.UpsertPeer(p)
			changed = true
		}
	}
	if changed {
		e.notifyPeerUpdate()
	}
}

// SyncGame pings peers and then syncs one game with everyone online.
func (e *Engine) SyncGame(ctx context.Context, gameID string) (map[string]syncengine.Result, error) {
	e.PingPairedPeers(ctx)
	online := e.OnlinePeers()
	if len(online) == 0 {
		return nil, fmt.Errorf("no online peers available")
	}
	results, err := e.Sync.SyncGame(ctx, gameID, online)
	e.trackSyncOutcome(gameID, results)
	return results, err
}

// trackSyncOutcome queues a game for automatic retry if any peer's sync
// failed (a transient/network error), or clears it once the sync completes.
func (e *Engine) trackSyncOutcome(gameID string, results map[string]syncengine.Result) {
	failed := false
	for _, r := range results {
		if r.Status == "error" {
			failed = true
			break
		}
	}
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()
	if e.pendingResync == nil {
		e.pendingResync = map[string]bool{}
	}
	if failed {
		if !e.pendingResync[gameID] {
			e.Log("info", fmt.Sprintf("sync for %s was interrupted; will retry automatically when reachable", gameID))
		}
		e.pendingResync[gameID] = true
	} else {
		delete(e.pendingResync, gameID)
	}
}

// StartResyncLoop runs two background safeties on one ticker:
//
//   - Every tick: retry any game whose sync was interrupted (network blip
//     mid-transfer) until it completes.
//   - Every reconcileEveryNTicks ticks: a full reconcile — sync every game
//     with online peers. This is the eventual-consistency backstop that
//     catches changes no event delivered (a missed watcher event, a dropped
//     fire-and-forget push trigger, or briefly stale peer status), which is
//     why cross-device changes could occasionally go undetected.
func (e *Engine) StartResyncLoop() {
	e.pendingMu.Lock()
	if e.stopRetry != nil { // already running
		e.pendingMu.Unlock()
		return
	}
	e.stopRetry = make(chan struct{})
	stop := e.stopRetry
	e.pendingMu.Unlock()

	go func() {
		ticker := time.NewTicker(resyncRetryInterval)
		defer ticker.Stop()
		ticks := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ticks++
				e.retryPendingResyncs()
				if ticks%reconcileEveryNTicks == 0 {
					e.reconcileAllGames()
				}
			}
		}
	}()
}

// reconcileAllGames refreshes peer status and re-syncs every game — the
// periodic backstop against missed sync triggers.
func (e *Engine) reconcileAllGames() {
	e.PingPairedPeers(context.Background())
	if len(e.OnlinePeers()) == 0 {
		return
	}
	e.SyncAllGames(context.Background())
}

func (e *Engine) retryPendingResyncs() {
	e.pendingMu.Lock()
	ids := make([]string, 0, len(e.pendingResync))
	for id := range e.pendingResync {
		ids = append(ids, id)
	}
	e.pendingMu.Unlock()
	if len(ids) == 0 {
		return
	}
	// Refresh LAN peer status first so a peer that just reconnected is seen
	// as online without waiting for the next discovery cycle.
	e.PingPairedPeers(context.Background())
	online := e.OnlinePeers()
	if len(online) == 0 {
		return // no one to sync with yet; keep waiting
	}
	for _, id := range ids {
		e.Log("info", fmt.Sprintf("retrying interrupted sync for %s", id))
		results, err := e.Sync.SyncGame(context.Background(), id, online)
		if err == nil {
			e.trackSyncOutcome(id, results) // clears on success
			e.pendingMu.Lock()
			done := !e.pendingResync[id]
			e.pendingMu.Unlock()
			if done {
				e.Log("success", fmt.Sprintf("re-synced %s after an earlier interruption", id))
			}
		}
	}
}

// SyncAllGames syncs every tracked game (used when a peer comes online).
func (e *Engine) SyncAllGames(ctx context.Context) {
	games, err := e.Store.ListGames()
	if err != nil {
		return
	}
	online := e.OnlinePeers()
	if len(online) == 0 {
		return
	}
	for _, g := range games {
		if !g.AutoSync {
			continue
		}
		results, err := e.Sync.SyncGame(ctx, g.ID, online)
		if err != nil {
			e.Log("warn", fmt.Sprintf("auto-sync %s: %v", g.ID, err))
			continue
		}
		e.trackSyncOutcome(g.ID, results)
	}
}

// InitiatePair sends a handshake to a device at address:port and opens the
// approve-confirm grace window.
func (e *Engine) InitiatePair(ctx context.Context, address string, port int) error {
	settings, err := e.Store.GetSettings()
	if err != nil {
		return err
	}

	e.Pairing.RecordSent(address, fmt.Sprintf("%s:%d", address, port))

	err = postHandshake(ctx, address, port, map[string]any{
		"peerId":     settings.NodeID,
		"deviceName": settings.DeviceName,
		"deviceType": settings.DeviceType,
		"port":       settings.Port,
	})
	if err != nil {
		return fmt.Errorf("handshake to %s:%d: %w", address, port, err)
	}
	e.Log("info", fmt.Sprintf("pairing request sent to %s:%d — waiting for their approval", address, port))
	return nil
}

// InitiatePairWan sends a handshake to a room member through the relay.
func (e *Engine) InitiatePairWan(ctx context.Context, peerID string) error {
	settings, err := e.Store.GetSettings()
	if err != nil {
		return err
	}
	// "relay" is the JS grace-window key for WAN-initiated handshakes.
	e.Pairing.RecordSent(peerID, "relay")

	_, err = e.Wan.Request(ctx, peerID, "/handshake", "POST", map[string]any{
		"peerId":     settings.NodeID,
		"deviceName": settings.DeviceName,
		"deviceType": settings.DeviceType,
		"port":       settings.Port,
	})
	if err != nil {
		return fmt.Errorf("WAN handshake to %s: %w", peerID, err)
	}
	e.Log("info", fmt.Sprintf("WAN pairing request sent to %s — waiting for their approval", peerID))
	return nil
}

// ApprovePairing accepts a pending incoming handshake: persists the peer
// and sends approve-confirm back to them.
func (e *Engine) ApprovePairing(ctx context.Context, peerID string) error {
	req, ok := e.Pairing.TakeIncoming(peerID)
	if !ok {
		return fmt.Errorf("no pending pairing request from %q", peerID)
	}
	settings, err := e.Store.GetSettings()
	if err != nil {
		return err
	}

	if err := e.Store.UpsertPeer(store.Peer{
		ID: req.PeerID, Name: req.DeviceName, DeviceType: orDefault(req.DeviceType, "desktop"),
		Address: req.Address, Port: req.Port, Status: "online", LastSeenMs: time.Now().UnixMilli(),
	}); err != nil {
		return err
	}
	// Same machine, fresh identity (reinstall/reset) — drop the ghost entry.
	if removed, _ := e.Store.PrunePeersAtAddress(req.Address, req.Port, req.PeerID); len(removed) > 0 {
		e.Log("info", fmt.Sprintf("removed stale pairing %v — same device re-paired with a new identity", removed))
	}

	confirmBody := map[string]any{
		"peerId":     settings.NodeID,
		"deviceName": settings.DeviceName,
		"deviceType": settings.DeviceType,
		"port":       settings.Port,
	}
	if req.IsWan || req.Address == "relay" {
		if _, err := e.Wan.Request(ctx, req.PeerID, "/approve-confirm", "POST", confirmBody); err != nil {
			e.Log("warn", fmt.Sprintf("WAN approve-confirm to %s failed (peer saved anyway): %v", req.DeviceName, err))
		}
	} else if err := postApproveConfirm(ctx, req.Address, req.Port, confirmBody); err != nil {
		e.Log("warn", fmt.Sprintf("approve-confirm to %s failed (peer saved anyway): %v", req.DeviceName, err))
	}

	e.Log("success", fmt.Sprintf("paired with %q (%s:%d)", req.DeviceName, req.Address, req.Port))
	e.notifyPeerUpdate()
	return nil
}

// RejectPairing discards a pending incoming handshake.
func (e *Engine) RejectPairing(peerID string) {
	e.Pairing.TakeIncoming(peerID)
	e.notifyPeerUpdate()
}

// Unpair removes a paired peer and proactively tells them, so the other
// device stops treating us as paired immediately instead of ghost-syncing
// until its next hello gets rejected.
func (e *Engine) Unpair(peerID string) error {
	peer, peerErr := e.Store.GetPeer(peerID)

	if err := e.Store.UnpairPeer(peerID); err != nil {
		return err
	}
	e.notifyPeerUpdate()

	// Best-effort notification — the peer may be offline, which is fine:
	// the reactive unpair-notify (on their next hello) still covers them.
	if peerErr == nil {
		go func() {
			settings, err := e.Store.GetSettings()
			if err != nil {
				return
			}
			if peer.Address == "relay" {
				e.Wan.SendRelayMessage(RelayMessage{Type: "unpair-notify", To: peerID, From: settings.NodeID})
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body, _ := json.Marshal(map[string]string{"peerId": settings.NodeID})
			url := fmt.Sprintf("http://%s:%d/api/p2p/unpair", peer.Address, peer.Port)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}()
	}
	return nil
}

// ClearPendingResync drops a game from the failsafe retry queue — called
// when it is untracked so the loop stops chasing a game we no longer hold.
func (e *Engine) ClearPendingResync(gameID string) {
	e.pendingMu.Lock()
	delete(e.pendingResync, gameID)
	e.pendingMu.Unlock()
}

// NotifyUntrack / NotifyRetrack tell every paired peer that a game was
// untracked / re-tracked here, so the change registers on their side too
// (untrack removes it there; retrack clears their tombstone so a following
// sync-on-track re-populates it). Best-effort and async — offline peers
// miss it, which is fine: the sync engine handles a one-sided state
// gracefully (peer_missing, no retry spam).
func (e *Engine) NotifyUntrack(gameID string) { e.notifyPeersGameOp("untrack", gameID) }
func (e *Engine) NotifyRetrack(gameID string) { e.notifyPeersGameOp("retrack", gameID) }

func (e *Engine) notifyPeersGameOp(op, gameID string) {
	peers, err := e.Store.ListPeers()
	if err != nil {
		return
	}
	settings, err := e.Store.GetSettings()
	if err != nil {
		return
	}
	for _, peer := range peers {
		peer := peer
		go func() {
			if peer.Address == "relay" {
				e.Wan.SendRelayMessage(RelayMessage{
					Type: op + "-notify", To: peer.ID, From: settings.NodeID, GameID: gameID,
				})
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body, _ := json.Marshal(map[string]string{"peerId": settings.NodeID, "gameId": gameID})
			url := fmt.Sprintf("http://%s:%d/api/p2p/%s", peer.Address, peer.Port, op)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}()
	}
}

// applyPeerUntrack / applyPeerRetrack mirror a peer's game op locally.
func (e *Engine) applyPeerUntrack(gameID string) {
	if e.OnUntrackRequest != nil {
		e.OnUntrackRequest(gameID)
	}
	e.notifyGamesUpdate()
}

func (e *Engine) applyPeerRetrack(gameID string) {
	if e.OnRetrackRequest != nil {
		e.OnRetrackRequest(gameID)
	}
}

func (e *Engine) notifyPeerUpdate() {
	if e.OnPeerUpdate != nil {
		e.OnPeerUpdate()
	}
}

func (e *Engine) notifyGamesUpdate() {
	if e.OnGamesUpdate != nil {
		e.OnGamesUpdate()
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
