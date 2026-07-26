package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/opensave/opensave/internal/p2p/syncengine"
)

// wanTransport tunnels the sync protocol through the relay RPC channel.
type wanTransport struct {
	wan *WanClient
}

func (t *wanTransport) FetchManifest(ctx context.Context, peer syncengine.Peer, gameID string, q syncengine.ManifestQuery) (syncengine.ManifestResponse, error) {
	params := url.Values{}
	if q.Name != "" {
		params.Set("name", q.Name)
	}
	if q.SavePath != "" {
		params.Set("savePath", q.SavePath)
	}
	params.Set("isFile", fmt.Sprintf("%t", q.IsFile))
	if q.AppID != "" {
		params.Set("appId", q.AppID)
	}
	if q.CoverURL != "" {
		params.Set("coverUrl", q.CoverURL)
	}

	raw, err := t.wan.Request(ctx, peer.ID, "/manifest/"+gameID+"?"+params.Encode(), "GET", nil)
	if err != nil {
		return syncengine.ManifestResponse{}, err
	}
	var resp syncengine.ManifestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return syncengine.ManifestResponse{}, fmt.Errorf("decode WAN manifest: %w", err)
	}
	return resp, nil
}

// slowLinkBytesPerSec is the worst throughput a block fetch is given before
// it's declared dead, used only to size the deadline and never to throttle.
//
// Pessimism has a cost on the other side: this deadline is also how long a
// request waits when the answer is never coming at all (a relay that shed the
// response, a peer that died mid-reply). Too generous and a lost message
// becomes a two-minute stall instead of a quick retry. 128 KB/s is roughly a
// 1 Mbps link — slow enough to cover a genuinely bad connection, fast enough
// that a dropped response is noticed in tens of seconds.
const slowLinkBytesPerSec = 128 << 10

func (t *wanTransport) FetchBlocks(ctx context.Context, peer syncengine.Peer, gameID, relPath string, blockIndices []int, blockSize int) ([]syncengine.BlockData, error) {
	// Give the request a deadline proportional to what it's actually moving,
	// so a big batch on a poor connection isn't cut off mid-flight.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && blockSize > 0 {
		expected := int64(len(blockIndices)) * int64(blockSize)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx,
			wanRequestTimeout+time.Duration(expected/slowLinkBytesPerSec)*time.Second)
		defer cancel()
	}

	raw, err := t.wan.Request(ctx, peer.ID, "/blocks/"+gameID, "POST", map[string]any{
		"relPath": relPath, "blockIndices": blockIndices, "blockSize": blockSize,
		// Peers that don't understand this ignore it and reply with raw
		// blocks, which decodeBlocks passes straight through.
		"encodings": gzipEncodings,
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Blocks []syncengine.BlockData `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode WAN blocks: %w", err)
	}
	return decodeBlocks(resp.Blocks)
}

func (t *wanTransport) DeleteRemote(ctx context.Context, peer syncengine.Peer, gameID, relPath string) error {
	_, err := t.wan.Request(ctx, peer.ID, "/delete-file/"+gameID, "POST", map[string]any{"relPath": relPath})
	return err
}

func (t *wanTransport) TriggerPeerPull(peer syncengine.Peer, gameID string) {
	t.wan.send(RelayMessage{
		Type: "request", To: peer.ID, From: t.wan.localPeerID(),
		Route: "/sync/trigger/" + gameID, Method: "GET",
	})
}

func (t *wanTransport) ReportSyncEvent(peer syncengine.Peer, gameID, eventType string, data map[string]any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	t.wan.send(RelayMessage{
		Type: "sync-event", To: peer.ID, From: t.wan.localPeerID(),
		GameID: gameID, EventType: eventType, Data: raw,
	})
}

// routingTransport picks LAN or WAN per peer.
type routingTransport struct {
	lan syncengine.Transport
	wan syncengine.Transport
}

func (t *routingTransport) pick(peer syncengine.Peer) syncengine.Transport {
	if peer.Wan() {
		return t.wan
	}
	return t.lan
}

func (t *routingTransport) FetchManifest(ctx context.Context, peer syncengine.Peer, gameID string, q syncengine.ManifestQuery) (syncengine.ManifestResponse, error) {
	return t.pick(peer).FetchManifest(ctx, peer, gameID, q)
}

func (t *routingTransport) FetchBlocks(ctx context.Context, peer syncengine.Peer, gameID, relPath string, blockIndices []int, blockSize int) ([]syncengine.BlockData, error) {
	return t.pick(peer).FetchBlocks(ctx, peer, gameID, relPath, blockIndices, blockSize)
}

func (t *routingTransport) DeleteRemote(ctx context.Context, peer syncengine.Peer, gameID, relPath string) error {
	return t.pick(peer).DeleteRemote(ctx, peer, gameID, relPath)
}

func (t *routingTransport) TriggerPeerPull(peer syncengine.Peer, gameID string) {
	t.pick(peer).TriggerPeerPull(peer, gameID)
}

func (t *routingTransport) ReportSyncEvent(peer syncengine.Peer, gameID, eventType string, data map[string]any) {
	t.pick(peer).ReportSyncEvent(peer, gameID, eventType, data)
}
