package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/cloud"
	"github.com/opensave/opensave/internal/snapshot"
)

// pendingPKCE holds verifier state between /api/auth/start and
// /api/auth/callback (one flow at a time, like the JS popup model).
var pendingPKCE = struct {
	sync.Mutex
	provider string
	verifier string
}{}

func (s *Server) cloudRoutes(r chi.Router) {
	r.Post("/api/auth/start", s.handleAuthStart)
	r.Post("/api/auth/callback", s.handleAuthCallback)
	r.Post("/api/auth/disconnect", s.handleAuthDisconnect)

	r.Get("/api/cloud/browse", s.handleCloudBrowse)
	r.Get("/api/cloud/snapshots/{gameId}", s.handleCloudSnapshots)
	r.Post("/api/cloud/restore/{gameId}", s.handleCloudRestore)
	r.Post("/api/cloud/sync-local/{gameId}", s.handleCloudSyncLocal)
}

// handleCloudBrowse lists every cloud snapshot the provider holds, grouped
// by game, so the UI can present a browsable explorer rather than a flat
// per-game list. Games with no cloud snapshots are omitted.
func (s *Server) handleCloudBrowse(w http.ResponseWriter, r *http.Request) {
	files, err := s.Daemon.Cloud.List()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	type remoteSnap struct {
		cloud.CloudFile
		Branch     string `json:"branch"`
		SnapshotID string `json:"snapshotId"`
	}
	type gameGroup struct {
		GameID    string       `json:"gameId"`
		GameName  string       `json:"gameName"`
		Count     int          `json:"count"`
		TotalSize int64        `json:"totalSize"`
		Snapshots []remoteSnap `json:"snapshots"`
	}

	groups := map[string]*gameGroup{}
	order := []string{}
	for _, f := range files {
		gameID, branch, snapID, ok := snapshot.ParseExportEntryName(f.Name)
		if !ok {
			continue
		}
		g, exists := groups[gameID]
		if !exists {
			name := gameID
			if game, err := s.Daemon.Store.GetGame(gameID); err == nil && game.Name != "" {
				name = game.Name
			}
			g = &gameGroup{GameID: gameID, GameName: name}
			groups[gameID] = g
			order = append(order, gameID)
		}
		g.Snapshots = append(g.Snapshots, remoteSnap{CloudFile: f, Branch: branch, SnapshotID: snapID})
		g.Count++
		g.TotalSize += f.SizeBytes
	}

	out := make([]*gameGroup, 0, len(order))
	for _, id := range order {
		out = append(out, groups[id])
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAuthStart begins a PKCE flow: returns the provider authorize URL
// for the UI to open (Wails opens it in an auth window in Phase 4; a
// browser works too).
func (s *Server) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if err := readJSON(r, &body); err != nil || body.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}

	verifier, challenge := cloud.GeneratePKCE()
	authURL, err := s.Daemon.Cloud.AuthURL(body.Provider, challenge)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	pendingPKCE.Lock()
	pendingPKCE.provider = body.Provider
	pendingPKCE.verifier = verifier
	pendingPKCE.Unlock()

	// Try to catch the redirect automatically (the registered redirect URI
	// is http://localhost/callback). When this works, sign-in completes
	// with no copy/paste; otherwise the UI falls back to manual code entry.
	auto := s.startAuthCallback()

	writeJSON(w, http.StatusOK, map[string]any{"authUrl": authURL, "autoCallback": auto})
}

// handleAuthCallback finishes the flow with the code captured from the
// redirect.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &body); err != nil || body.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	pendingPKCE.Lock()
	provider, verifier := pendingPKCE.provider, pendingPKCE.verifier
	pendingPKCE.provider, pendingPKCE.verifier = "", ""
	pendingPKCE.Unlock()
	if provider == "" {
		writeError(w, http.StatusBadRequest, "no auth flow in progress — call /api/auth/start first")
		return
	}

	if err := s.Daemon.Cloud.ExchangeAuthCode(provider, body.Code, verifier); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	cfg, _ := s.Daemon.Store.GetCloudConfig()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "userEmail": cfg.UserEmail})
}

func (s *Server) handleAuthDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.Daemon.Cloud.Disconnect(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// handleCloudSnapshots lists remote snapshots belonging to one game
// (names encode gameId__branch__snapId.zip).
func (s *Server) handleCloudSnapshots(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	files, err := s.Daemon.Cloud.List()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	type remoteSnap struct {
		cloud.CloudFile
		Branch     string `json:"branch"`
		SnapshotID string `json:"snapshotId"`
	}
	matches := []remoteSnap{}
	for _, f := range files {
		g, branch, snapID, ok := snapshot.ParseExportEntryName(f.Name)
		if !ok || g != gameID {
			continue
		}
		matches = append(matches, remoteSnap{CloudFile: f, Branch: branch, SnapshotID: snapID})
	}
	writeJSON(w, http.StatusOK, matches)
}

// handleCloudRestore downloads a remote snapshot zip, registers it, and
// restores it over the save.
func (s *Server) handleCloudRestore(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		FileName string `json:"fileName"`
	}
	if err := readJSON(r, &body); err != nil || body.FileName == "" {
		writeError(w, http.StatusBadRequest, "fileName is required")
		return
	}

	g, branch, snapID, ok := snapshot.ParseExportEntryName(body.FileName)
	if !ok || g != gameID {
		writeError(w, http.StatusBadRequest, "fileName does not belong to this game")
		return
	}
	if _, err := s.Daemon.Store.GetGame(gameID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	settings, err := s.Daemon.Store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	destDir := filepath.Join(settings.BackupsDir, gameID, branch)
	if err := os.MkdirAll(destDir, 0o777); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	destPath := filepath.Join(destDir, snapID+".zip")

	if err := s.Daemon.Cloud.Download(body.FileName, destPath); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	info, err := os.Stat(destPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Daemon.EnsureImportedSnapshot(gameID, branch, snapID, destPath, info.Size()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if _, err := s.Daemon.Snapshots.Restore(gameID, snapID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("downloaded but restore failed: %v", err))
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "snapshotId": snapID})
}

// handleCloudSyncLocal uploads every local snapshot of a game that the
// provider doesn't have yet.
func (s *Server) handleCloudSyncLocal(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	if _, err := s.Daemon.Store.GetGame(gameID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	remote, err := s.Daemon.Cloud.List()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	remoteNames := map[string]bool{}
	for _, f := range remote {
		remoteNames[f.Name] = true
	}

	branches, err := s.Daemon.Store.ListBranches(gameID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	uploaded, skipped := 0, 0
	for _, branch := range branches {
		snaps, err := s.Daemon.Store.ListSnapshots(gameID, branch)
		if err != nil {
			continue
		}
		for _, snap := range snaps {
			remoteName := fmt.Sprintf("%s__%s__%s.zip", gameID, branch, snap.ID)
			if remoteNames[remoteName] {
				skipped++
				continue
			}
			if err := s.Daemon.Cloud.Upload(snap.ZipPath, remoteName); err != nil {
				if strings.Contains(err.Error(), "not enabled") {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				s.Daemon.Log.Log("warn", fmt.Sprintf("upload %s failed: %v", remoteName, err))
				skipped++
				continue
			}
			uploaded++
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"uploaded": uploaded, "skipped": skipped})
}
