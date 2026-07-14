package presets

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// loadAppCache reads the persisted AppID->name cache; errors yield an
// empty cache (best-effort, same as JS).
func loadAppCache(cacheFile string) map[string]string {
	cache := map[string]string{}
	if cacheFile == "" {
		return cache
	}
	raw, err := os.ReadFile(cacheFile)
	if err != nil {
		return cache
	}
	_ = json.Unmarshal(raw, &cache)
	return cache
}

// saveAppCache persists the cache, best-effort.
func saveAppCache(cacheFile string, cache map[string]string) {
	if cacheFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o777); err != nil {
		return
	}
	raw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(cacheFile, raw, 0o666)
}

var steamAPIClient = &http.Client{Timeout: 3 * time.Second}

// fetchSteamAppName queries the Steam Store API for an AppID's title.
// Returns "" on any failure — the caller keeps the placeholder name.
func fetchSteamAppName(appID string) string {
	url := fmt.Sprintf("https://store.steampowered.com/api/appdetails?appids=%s&filters=basic", appID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	// The Store API rejects requests without a browser-like User-Agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := steamAPIClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var payload map[string]struct {
		Success bool `json:"success"`
		Data    struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	if entry, ok := payload[appID]; ok && entry.Success {
		return entry.Data.Name
	}
	return ""
}
