package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Resolving a divergence in one of a game's extra save locations.
//
// Separate from the whole-game route because the answers differ: there is no
// "keep both" here. That one parks the peer's copy on a branch, and branches
// belong to the game rather than to one of its folders — a button offering it
// would do something to the save folder while the user was looking at a
// settings folder.
func (s *Server) handleResolveRootConflict(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		PeerID     string `json:"peerId"`
		Root       string `json:"root"`
		Resolution string `json:"resolution"`
	}
	if err := readJSON(r, &body); err != nil || body.Resolution == "" || body.Root == "" {
		writeError(w, http.StatusBadRequest, "peerId, root and resolution are required")
		return
	}
	switch body.Resolution {
	case "keep-local", "keep-remote":
	default:
		writeError(w, http.StatusBadRequest, "invalid resolution "+body.Resolution+
			" — a save location can be resolved with keep-local or keep-remote")
		return
	}

	// Adopting the peer's copy can mean a long transfer, so the click returns
	// immediately and the outcome arrives over the WebSocket, exactly as
	// whole-game resolution does.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		err := s.Daemon.P2P.Sync.ResolveRootConflict(ctx, gameID, body.PeerID, body.Root, body.Resolution)

		payload := map[string]any{"gameId": gameID, "root": body.Root, "resolution": body.Resolution}
		if err != nil {
			payload["error"] = err.Error()
		}
		s.Hub.Broadcast("location-conflict-resolved", payload)
		s.BroadcastPeersUpdate()
		s.BroadcastGamesUpdate()
	}()

	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}
