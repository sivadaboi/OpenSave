package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/daemon"
	"github.com/opensave/opensave/internal/presets"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/sysintegration"
)

// routes registers the Phase 1 endpoint surface. Peer/cloud/p2p routes
// attach in Phases 2-3; window-control and dialog routes attach with the
// Wails app in Phase 4.
func (s *Server) routes(r chi.Router) {
	r.Get("/api/status", s.handleStatus)
	r.Get("/api/settings", s.handleGetSettings)
	r.Post("/api/settings", s.handleUpdateSettings)

	r.Get("/api/games", s.handleListGames)
	r.Post("/api/games", s.handleTrackGame)
	r.Patch("/api/games/{gameId}", s.handleUpdateGame)
	r.Delete("/api/games/{gameId}", s.handleUntrackGame)

	r.Post("/api/games/{gameId}/snapshot", s.handleCreateSnapshot)
	r.Post("/api/games/{gameId}/rollback", s.handleRollback)
	r.Get("/api/games/{gameId}/snapshot/{snapshotId}/files", s.handleSnapshotFiles)
	r.Post("/api/games/{gameId}/snapshot/{snapshotId}/restore-file", s.handleRestoreFile)
	r.Delete("/api/games/{gameId}/snapshot/{snapshotId}", s.handleDeleteSnapshot)

	r.Post("/api/games/{gameId}/branch", s.handleCreateBranch)
	r.Post("/api/games/{gameId}/branch/switch", s.handleSwitchBranch)
	r.Delete("/api/games/{gameId}/branch/{branch}", s.handleDeleteBranch)
	r.Post("/api/games/{gameId}/launch", s.handleLaunchGame)

	r.Post("/api/backup/export", s.handleBackupExport)
	r.Post("/api/backup/restore", s.handleBackupRestore)

	r.Post("/api/snapshots/prune", s.handlePruneSnapshots)

	r.Get("/api/presets/scan", s.handlePresetScan)

	s.peerRoutes(r)
	s.cloudRoutes(r)

	r.Get("/ws", s.Hub.ServeHTTP)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Daemon.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	games, _ := s.Daemon.Store.ListGames()
	peers, _ := s.Daemon.Store.ListPeers()
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":  settings,
		"gameCount": len(games),
		"peerCount": len(peers),
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsWire())
}

// cloudSyncPatch mirrors the JS settings.cloudSync sub-object on writes.
// Pointer fields distinguish "omitted" from zero values.
type cloudSyncPatch struct {
	Enabled             *bool             `json:"enabled"`
	Provider            *string           `json:"provider"`
	URL                 *string           `json:"url"`
	Username            *string           `json:"username"`
	Password            *string           `json:"password"`
	Headers             *string           `json:"headers"`
	FolderID            *string           `json:"folderId"`
	CustomClientIDs     map[string]string `json:"customClientIds"`
	CustomClientSecrets map[string]string `json:"customClientSecrets"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	current, err := s.Daemon.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Read the raw body once: settings fields decode over the current
	// values (the JS {...current, ...new} merge semantics); cloudSync is
	// peeled off and applied to the cloud config separately.
	var raw json.RawMessage
	if err := readJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var withCloud struct {
		CloudSync *cloudSyncPatch `json:"cloudSync"`
	}
	_ = json.Unmarshal(raw, &withCloud)
	if patch := withCloud.CloudSync; patch != nil {
		if err := s.applyCloudPatch(patch); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	prevSyncCode := ""
	prevRelayURL := ""
	prevStartOnBoot := false
	prevHostRelay := false
	prevRelayPort := 0
	if prev, err := s.Daemon.Store.GetSettings(); err == nil {
		prevSyncCode, prevRelayURL, prevStartOnBoot = prev.SyncCode, prev.RelayURL, prev.StartOnBoot
		prevHostRelay, prevRelayPort = prev.HostRelay, prev.RelayPort
	}

	if err := s.Daemon.Store.UpdateSettings(current); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := s.Daemon.Store.GetSettings()

	// Relay/room changes take effect immediately.
	if updated.SyncCode != prevSyncCode || updated.RelayURL != prevRelayURL {
		s.Daemon.P2P.Wan.Connect()
	}
	// Start-on-boot toggling registers/unregisters with the OS.
	if updated.StartOnBoot != prevStartOnBoot {
		if err := sysintegration.SetAutostart(updated.StartOnBoot); err != nil {
			s.Daemon.Log.Log("warn", "start-on-boot change failed: "+err.Error())
		}
	}
	// Host-relay toggle / port change starts or stops the in-process relay.
	if updated.HostRelay != prevHostRelay || updated.RelayPort != prevRelayPort {
		s.Daemon.P2P.ApplyRelayHosting(updated.HostRelay, updated.RelayPort)
	}

	writeJSON(w, http.StatusOK, s.settingsWire())
}

// applyCloudPatch merges a cloudSync write into the cloud config row,
// preserving stored OAuth tokens (the UI never sends them back).
func (s *Server) applyCloudPatch(patch *cloudSyncPatch) error {
	cfg, err := s.Daemon.Store.GetCloudConfig()
	if err != nil {
		return err
	}
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.Provider != nil {
		cfg.Provider = *patch.Provider
	}
	if patch.URL != nil {
		cfg.URL = *patch.URL
	}
	if patch.Username != nil {
		cfg.Username = *patch.Username
	}
	if patch.Password != nil {
		cfg.Password = *patch.Password
	}
	if patch.Headers != nil {
		cfg.HeadersJSON = *patch.Headers
	}
	if patch.FolderID != nil {
		cfg.FolderID = *patch.FolderID
	}
	if patch.CustomClientIDs != nil {
		if cfg.CustomClientIDs == nil {
			cfg.CustomClientIDs = map[string]string{}
		}
		for k, v := range patch.CustomClientIDs {
			cfg.CustomClientIDs[k] = v
		}
	}
	if patch.CustomClientSecrets != nil {
		if cfg.CustomClientSecrets == nil {
			cfg.CustomClientSecrets = map[string]string{}
		}
		for k, v := range patch.CustomClientSecrets {
			cfg.CustomClientSecrets[k] = v
		}
	}
	return s.Daemon.Store.UpdateCloudConfig(cfg)
}

func (s *Server) handleListGames(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.gamesPayload())
}

// handlePruneSnapshots cleans up old snapshots across all games and every
// branch. With applyDefaultToAll, it first sets every game's retention
// limit to the global default (so the cleanup uses the new limit). Returns
// how many snapshots were removed and the disk space freed.
func (s *Server) handlePruneSnapshots(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ApplyDefaultToAll bool `json:"applyDefaultToAll"`
	}
	_ = readJSON(r, &body)

	if body.ApplyDefaultToAll {
		settings, err := s.Daemon.Store.GetSettings()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		limit := settings.DefaultMaxSnapshots
		games, err := s.Daemon.Store.ListGames()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, g := range games {
			if g.MaxSnapshots != limit {
				g.MaxSnapshots = limit
				_ = s.Daemon.Store.UpdateGame(g)
			}
		}
	}

	removed, freed, err := s.Daemon.Snapshots.PruneAllGames()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Daemon.Log.Log("success", fmt.Sprintf("snapshot cleanup: removed %d snapshot(s), freed %.1f MB", removed, float64(freed)/(1<<20)))
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "freedBytes": freed})
}

func (s *Server) handleTrackGame(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		SavePath string `json:"savePath"`
		AppID    string `json:"appId"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Name == "" || body.SavePath == "" {
		writeError(w, http.StatusBadRequest, "name and savePath are required")
		return
	}

	game, err := s.Daemon.TrackGame(store.Game{Name: body.Name, SavePath: body.SavePath, AppID: body.AppID})
	if err != nil {
		// Duplicates (id or path) are conflicts; anything else the daemon
		// rejects is bad input.
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already track") || strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, s.gamePayload(game))
}

func (s *Server) handleUpdateGame(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	game, err := s.Daemon.Store.GetGame(gameID)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}

	oldSavePath := game.SavePath
	oldAutoSync := game.AutoSync
	if err := readJSON(r, &game); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	game.ID = gameID // id is not client-mutable
	// Cover art: a user-set custom URL is always kept. An empty cover, or
	// a previously auto-generated Steam cover, is (re)derived from the
	// AppID — so changing the AppID refreshes the art.
	if game.CoverURL == "" || isSteamCover(game.CoverURL) {
		game.CoverURL = daemon.SteamCoverURL(game.AppID)
	}

	if err := s.Daemon.Store.UpdateGame(game); err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}

	// Re-watch if the save location or autoSync flag changed.
	if game.SavePath != oldSavePath || game.AutoSync != oldAutoSync {
		s.Daemon.Watcher.Unwatch(gameID)
		if game.AutoSync {
			if err := s.Daemon.Watcher.Watch(gameID, game.SavePath); err != nil {
				s.Daemon.Log.Log("warn", "re-watch failed: "+err.Error())
			}
		}
	}

	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, s.gamePayload(game))
}

// isSteamCover reports whether a cover URL is an auto-generated Steam CDN
// header image (as opposed to a user's custom cover).
func isSteamCover(url string) bool {
	return strings.Contains(url, "steamstatic.com/steam/apps/")
}

func (s *Server) handleUntrackGame(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	if err := s.Daemon.UntrackGame(gameID); err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		Comment string `json:"comment"`
	}
	_ = readJSON(r, &body) // empty body is fine

	snap, err := s.Daemon.Snapshots.Create(gameID, body.Comment, false)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		SnapshotID string `json:"snapshotId"`
	}
	if err := readJSON(r, &body); err != nil || body.SnapshotID == "" {
		writeError(w, http.StatusBadRequest, "snapshotId is required")
		return
	}

	snap, err := s.Daemon.Snapshots.Restore(gameID, body.SnapshotID)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleCreateBranch(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	clean, err := s.Daemon.Snapshots.CreateBranch(gameID, body.Name)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]string{"name": clean})
}

func (s *Server) handleSwitchBranch(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := s.Daemon.Snapshots.SwitchBranch(gameID, body.Name); err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// handleDeleteSnapshot removes a single snapshot (metadata + zip).
func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	snapshotID := chi.URLParam(r, "snapshotId")
	freed, err := s.Daemon.Snapshots.DeleteSnapshot(gameID, snapshotID)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}
	s.Daemon.Log.Log("info", fmt.Sprintf("deleted snapshot %s (%.1f MB)", snapshotID, float64(freed)/(1<<20)))
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"freedBytes": freed})
}

// handleDeleteBranch removes a branch and all its snapshots. The active
// branch and "main" can't be deleted.
func (s *Server) handleDeleteBranch(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	branch := chi.URLParam(r, "branch")
	if branch == "main" {
		writeError(w, http.StatusBadRequest, "the main branch can't be deleted")
		return
	}
	game, err := s.Daemon.Store.GetGame(gameID)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}
	if branch == game.ActiveBranch {
		writeError(w, http.StatusBadRequest, "switch to another branch before deleting this one")
		return
	}
	removed, freed := s.Daemon.Snapshots.DeleteBranch(gameID, branch)
	s.Daemon.Log.Log("info", fmt.Sprintf("deleted branch %q of %q (%d snapshot(s), %.1f MB)", branch, game.Name, removed, float64(freed)/(1<<20)))
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "freedBytes": freed})
}

func (s *Server) handlePresetScan(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Daemon.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := s.Daemon.Scanner.Scan(settings.CustomScanPaths)
	if found == nil {
		found = []presets.DiscoveredSave{} // never null on the wire
	}
	writeJSON(w, http.StatusOK, found)
}
