package p2p

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opensave/opensave/internal/version"
)

// Peer-to-peer app updates: a paired peer running a newer build serves its
// own executable, so the other device can update in one click instead of
// someone shuttling the exe around. LAN peers stream it over HTTP; WAN
// peers pull base64 chunks through the relay (which caps message sizes).

// PeerBuild identifies the app build a peer is running.
type PeerBuild struct {
	AppVersion  string `json:"appVersion"`
	BuildTimeMs int64  `json:"buildTimeMs"`
}

// recordPeerBuild stores the latest build info a peer reported (via LAN
// ping responses or WAN presence messages). In-memory only — it's live
// state, refreshed within seconds of any contact.
func (e *Engine) recordPeerBuild(peerID, appVersion string, buildTimeMs int64) {
	e.buildMu.Lock()
	if e.peerBuilds == nil {
		e.peerBuilds = map[string]PeerBuild{}
	}
	e.peerBuilds[peerID] = PeerBuild{AppVersion: appVersion, BuildTimeMs: buildTimeMs}
	e.buildMu.Unlock()
}

// PeerBuilds returns a copy of the known per-peer build info.
func (e *Engine) PeerBuilds() map[string]PeerBuild {
	e.buildMu.Lock()
	defer e.buildMu.Unlock()
	out := make(map[string]PeerBuild, len(e.peerBuilds))
	for id, b := range e.peerBuilds {
		out[id] = b
	}
	return out
}

// runningExeSHA hashes this process's executable once (it can't change
// while running) for transfer integrity checks.
var runningExeSHA = sync.OnceValues(func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
})

// handleAppBinary streams this device's running executable to a paired
// LAN peer (mounted behind requirePairedPeer).
func (e *Engine) handleAppBinary(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.Open(exe)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sha, err := runningExeSHA()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	e.Log("info", "serving app binary to paired peer "+clientIP(r))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-App-Version", version.Version)
	w.Header().Set("X-Build-Time-Ms", strconv.FormatInt(version.BuildTimeMs(), 10))
	w.Header().Set("X-App-Sha256", sha)
	_, _ = io.Copy(w, f)
}

// wanBinaryChunkSize keeps each relay message ~1MB after base64 — safely
// under the relay's 16MB read limit and comparable to block batches.
const wanBinaryChunkSize = 768 << 10

// serveAppBinaryChunk answers a WAN "/app-binary?offset=N" request with one
// base64 chunk plus the metadata needed to verify the assembled file.
func serveAppBinaryChunk(route string) (int, any) {
	u, err := url.Parse(route)
	if err != nil {
		return 400, map[string]string{"error": "bad route"}
	}
	offset, _ := strconv.ParseInt(u.Query().Get("offset"), 10, 64)
	if offset < 0 {
		offset = 0
	}

	exe, err := os.Executable()
	if err != nil {
		return 500, map[string]string{"error": err.Error()}
	}
	f, err := os.Open(exe)
	if err != nil {
		return 500, map[string]string{"error": err.Error()}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 500, map[string]string{"error": err.Error()}
	}
	sha, err := runningExeSHA()
	if err != nil {
		return 500, map[string]string{"error": err.Error()}
	}

	buf := make([]byte, wanBinaryChunkSize)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return 500, map[string]string{"error": err.Error()}
	}

	return 200, map[string]any{
		"base64Data":  base64.StdEncoding.EncodeToString(buf[:n]),
		"offset":      offset,
		"totalSize":   info.Size(),
		"sha256":      sha,
		"appVersion":  version.Version,
		"buildTimeMs": version.BuildTimeMs(),
	}
}

// DownloadPeerBinary fetches a paired peer's running executable into
// destPath, verifying the SHA-256 the peer reports. Returns the peer's
// build identity on success.
func (e *Engine) DownloadPeerBinary(ctx context.Context, peerID, destPath string, progress func(done, total int64)) (PeerBuild, error) {
	peer, err := e.Store.GetPeer(peerID)
	if err != nil {
		return PeerBuild{}, fmt.Errorf("peer not found: %w", err)
	}
	if peer.Address == "relay" {
		return e.downloadPeerBinaryWAN(ctx, peerID, destPath, progress)
	}
	return e.downloadPeerBinaryLAN(ctx, peer.Address, peer.Port, destPath, progress)
}

func (e *Engine) downloadPeerBinaryLAN(ctx context.Context, address string, port int, destPath string, progress func(done, total int64)) (PeerBuild, error) {
	url := fmt.Sprintf("http://%s:%d/api/p2p/app-binary", address, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return PeerBuild{}, err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		return PeerBuild{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return PeerBuild{}, fmt.Errorf("peer returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	build := PeerBuild{AppVersion: resp.Header.Get("X-App-Version")}
	build.BuildTimeMs, _ = strconv.ParseInt(resp.Header.Get("X-Build-Time-Ms"), 10, 64)
	wantSHA := resp.Header.Get("X-App-Sha256")

	out, err := os.Create(destPath)
	if err != nil {
		return PeerBuild{}, err
	}
	h := sha256.New()
	total := resp.ContentLength
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, err := out.Write(chunk); err != nil {
				out.Close()
				return PeerBuild{}, err
			}
			h.Write(chunk)
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			return PeerBuild{}, readErr
		}
	}
	if err := out.Close(); err != nil {
		return PeerBuild{}, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); wantSHA != "" && got != wantSHA {
		return PeerBuild{}, fmt.Errorf("transfer corrupted: checksum mismatch")
	}
	return build, nil
}

func (e *Engine) downloadPeerBinaryWAN(ctx context.Context, peerID, destPath string, progress func(done, total int64)) (PeerBuild, error) {
	out, err := os.Create(destPath)
	if err != nil {
		return PeerBuild{}, err
	}
	defer out.Close()

	h := sha256.New()
	var build PeerBuild
	var wantSHA string
	var offset, total int64
	for {
		raw, err := e.Wan.Request(ctx, peerID, fmt.Sprintf("/app-binary?offset=%d", offset), "GET", nil)
		if err != nil {
			return PeerBuild{}, err
		}
		var resp struct {
			Base64Data  string `json:"base64Data"`
			TotalSize   int64  `json:"totalSize"`
			SHA256      string `json:"sha256"`
			AppVersion  string `json:"appVersion"`
			BuildTimeMs int64  `json:"buildTimeMs"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return PeerBuild{}, err
		}
		if offset == 0 {
			build = PeerBuild{AppVersion: resp.AppVersion, BuildTimeMs: resp.BuildTimeMs}
			wantSHA = resp.SHA256
			total = resp.TotalSize
		}
		chunk, err := base64.StdEncoding.DecodeString(resp.Base64Data)
		if err != nil {
			return PeerBuild{}, fmt.Errorf("decode chunk at %d: %w", offset, err)
		}
		if len(chunk) == 0 {
			break
		}
		if _, err := out.Write(chunk); err != nil {
			return PeerBuild{}, err
		}
		h.Write(chunk)
		offset += int64(len(chunk))
		if progress != nil {
			progress(offset, total)
		}
		if offset >= total {
			break
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); wantSHA != "" && got != wantSHA {
		return PeerBuild{}, fmt.Errorf("transfer corrupted: checksum mismatch")
	}
	return build, nil
}
