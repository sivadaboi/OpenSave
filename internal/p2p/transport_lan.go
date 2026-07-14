package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/internal/store"
)

// lanTransport speaks the /api/p2p/* HTTP protocol directly to a peer.
// WAN peers get their own relay-tunnel transport in Phase 3.
type lanTransport struct{}

var lanClient = &http.Client{Timeout: 30 * time.Second}

func peerURL(peer syncengine.Peer, route string) string {
	return fmt.Sprintf("http://%s:%d/api/p2p%s", peer.Address, peer.Port, route)
}

func (t *lanTransport) FetchManifest(ctx context.Context, peer syncengine.Peer, gameID string, q syncengine.ManifestQuery) (syncengine.ManifestResponse, error) {
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

	var resp syncengine.ManifestResponse
	err := t.getJSON(ctx, peerURL(peer, "/manifest/"+gameID)+"?"+params.Encode(), &resp)
	return resp, err
}

func (t *lanTransport) FetchBlocks(ctx context.Context, peer syncengine.Peer, gameID, relPath string, blockIndices []int, blockSize int) ([]syncengine.BlockData, error) {
	var resp struct {
		Blocks []syncengine.BlockData `json:"blocks"`
	}
	err := t.postJSON(ctx, peerURL(peer, "/blocks/"+gameID), map[string]any{
		"relPath": relPath, "blockIndices": blockIndices, "blockSize": blockSize,
	}, &resp)
	return resp.Blocks, err
}

func (t *lanTransport) DeleteRemote(ctx context.Context, peer syncengine.Peer, gameID, relPath string) error {
	return t.postJSON(ctx, peerURL(peer, "/delete-file/"+gameID), map[string]any{"relPath": relPath}, nil)
}

func (t *lanTransport) TriggerPeerPull(peer syncengine.Peer, gameID string) {
	// Fire-and-forget, 5s cap, same as the JS fetch().catch(() => {}).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		url := fmt.Sprintf("http://%s:%d/api/sync/trigger/%s", peer.Address, peer.Port, gameID)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		resp, err := lanClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
}

func (t *lanTransport) ReportSyncEvent(peer syncengine.Peer, gameID, eventType string, data map[string]any) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = t.postJSON(ctx, peerURL(peer, "/sync-event/"+gameID), map[string]any{
			"eventType": eventType, "data": data,
		}, nil)
	}()
}

func (t *lanTransport) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return doJSON(req, out)
}

func (t *lanTransport) postJSON(ctx context.Context, url string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doJSON(req, out)
}

func doJSON(req *http.Request, out any) error {
	resp, err := lanClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("peer returned %d: %s", resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// pingPeer probes a paired peer's /api/p2p/ping. Reports reachability plus
// the peer's app build info (for the peer-update flow) when present.
func pingPeer(ctx context.Context, p store.Peer, fromNodeID string) (pingInfo, bool) {
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://%s:%d/api/p2p/ping?from=%s", p.Address, p.Port, url.QueryEscape(fromNodeID))
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, url, nil)
	if err != nil {
		return pingInfo{}, false
	}
	resp, err := lanClient.Do(req)
	if err != nil {
		return pingInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pingInfo{}, false
	}
	var info pingInfo
	_ = json.NewDecoder(resp.Body).Decode(&info)
	return info, true
}

// pingInfo is the subset of the ping response the caller records.
type pingInfo struct {
	AppVersion  string `json:"appVersion"`
	BuildTimeMs int64  `json:"buildTimeMs"`
}

func postHandshake(ctx context.Context, address string, port int, body map[string]any) error {
	return postPeerJSON(ctx, address, port, "/handshake", body)
}

func postApproveConfirm(ctx context.Context, address string, port int, body map[string]any) error {
	return postPeerJSON(ctx, address, port, "/approve-confirm", body)
}

func postPeerJSON(ctx context.Context, address string, port int, route string, body map[string]any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://%s:%d/api/p2p%s", address, port, route)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doJSON(req, nil)
}
