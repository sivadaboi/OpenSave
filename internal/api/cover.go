package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var coverClient = &http.Client{Timeout: 15 * time.Second}

// handleCover proxies (and disk-caches) Steam CDN cover art through the local
// daemon. The embedded webview can't reliably hotlink an external CDN, but it
// can always reach the local API — and the daemon has proven outbound
// connectivity (it talks to the relay). So covers load from localhost and
// keep working offline once cached.
//
// GET /api/cover?appId=<numeric>[&portrait=1]
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("appId")
	if !isNumericID(appID) {
		writeError(w, http.StatusBadRequest, "appId must be numeric")
		return
	}
	portrait := r.URL.Query().Get("portrait") == "1"

	data, err := s.cachedCover(appID, portrait)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(data)
}

// isNumericID guards against SSRF: only all-digit App IDs ever reach the CDN
// URL template.
func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// cachedCover returns cover bytes for an App ID, fetching from the Steam CDN
// on a cache miss. Portrait requests fall back to the landscape header when a
// title has no library art, so more games show something.
func (s *Server) cachedCover(appID string, portrait bool) ([]byte, error) {
	name := appID + ".jpg"
	if portrait {
		name = appID + "_p.jpg"
	}
	dir := filepath.Join(s.Daemon.Paths.HomeDir, "covers")
	path := filepath.Join(dir, name)

	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return data, nil
	}

	var files []string
	if portrait {
		files = []string{"library_600x900.jpg", "header.jpg"}
	} else {
		files = []string{"header.jpg"}
	}

	var lastErr error
	for _, f := range files {
		url := "https://cdn.cloudflare.steamstatic.com/steam/apps/" + appID + "/" + f
		data, err := fetchImage(url)
		if err != nil {
			lastErr = err
			continue
		}
		_ = os.MkdirAll(dir, 0o777)
		_ = os.WriteFile(path, data, 0o666)
		return data, nil
	}
	return nil, lastErr
}

func fetchImage(url string) ([]byte, error) {
	resp, err := coverClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover fetch %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MB cap
}
