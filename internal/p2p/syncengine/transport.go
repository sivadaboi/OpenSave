package syncengine

import (
	"context"

	"github.com/opensave/opensave/internal/delta"
)

// Peer identifies a sync counterpart. Address "relay" (or IsWan) means the
// peer is reachable only through the WAN relay.
type Peer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	IsWan   bool   `json:"isWan"`
}

// Wan reports whether traffic to this peer goes through the relay.
func (p Peer) Wan() bool { return p.IsWan || p.Address == "relay" }

// SnapshotInfo is the lightweight snapshot metadata exchanged in manifest
// responses.
type SnapshotInfo struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Comment   string `json:"comment"`
}

// ManifestResponse is what a peer returns for a manifest request.
type ManifestResponse struct {
	Manifest       delta.Manifest `json:"manifest"`
	ActiveBranch   string         `json:"activeBranch"`
	LatestSnapshot *SnapshotInfo  `json:"latestSnapshot"`
}

// ManifestQuery carries the game metadata that lets the remote side
// auto-track a game it doesn't know yet (same as the JS query params).
type ManifestQuery struct {
	Name     string
	SavePath string
	IsFile   bool
	// AppID and CoverURL let a peer that auto-tracks this game show the same
	// cover art, instead of a blank tile.
	AppID    string
	CoverURL string
}

// BlockData is one fetched block.
type BlockData struct {
	Index  int    `json:"index"`
	Data   []byte `json:"data"` // JSON-marshals to base64, matching the JS wire format
	Length int    `json:"length"`
}

// Transport moves sync protocol messages to a peer. The LAN implementation
// speaks HTTP to the peer's /api/p2p/* routes; the WAN implementation
// tunnels the same requests through the relay WebSocket.
type Transport interface {
	FetchManifest(ctx context.Context, peer Peer, gameID string, q ManifestQuery) (ManifestResponse, error)
	FetchBlocks(ctx context.Context, peer Peer, gameID, relPath string, blockIndices []int, blockSize int) ([]BlockData, error)
	DeleteRemote(ctx context.Context, peer Peer, gameID, relPath string) error
	TriggerPeerPull(peer Peer, gameID string)
	// ReportSyncEvent is fire-and-forget progress reporting to the peer.
	ReportSyncEvent(peer Peer, gameID, eventType string, data map[string]any)
}

// ProgressEvent feeds both the local dashboard and the remote peer's UI.
type ProgressEvent struct {
	PeerName         string  `json:"peerName"`
	Direction        string  `json:"direction,omitempty"`
	BytesTransferred int64   `json:"bytesTransferred,omitempty"`
	TotalBytes       int64   `json:"totalBytes,omitempty"`
	SpeedBytesPerSec float64 `json:"speedBytesPerSec,omitempty"`
	Percentage       int     `json:"percentage,omitempty"`
	Error            string  `json:"error,omitempty"`
}

// ProgressCallbacks are optional hooks into the dashboard WS hub.
type ProgressCallbacks struct {
	OnSyncStart    func(gameID string, ev ProgressEvent)
	OnSyncProgress func(gameID string, ev ProgressEvent)
	OnSyncComplete func(gameID string, ev ProgressEvent)
	OnSyncError    func(gameID string, ev ProgressEvent)
	OnConflict     func(gameID string)
}
