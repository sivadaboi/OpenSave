package api

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// A game's extra save locations: the folders beyond its main one that belong
// to the same title, so a save split between (say) AppData and Documents is
// one game rather than two.

func (s *Server) handleListGameRoots(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	roots, err := s.Daemon.Store.ListGameRoots(gameID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if roots == nil {
		roots = nil // marshals as [], not null, via the slice below
	}
	out := make([]map[string]any, 0, len(roots))
	for _, root := range roots {
		out = append(out, map[string]any{
			"name": root.Name,
			"path": root.Path,
			// Whether this device knows where the location actually is. A
			// location learned from a peer arrives named but not placed, and
			// the UI has to be able to say so and ask.
			"mapped": root.Mapped(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddGameRoot(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.Daemon.Store.GetGame(gameID); err != nil {
		writeError(w, http.StatusNotFound, "Game not found.")
		return
	}
	// Rejections here are the overlap and naming rules, which are the user's
	// to hear about rather than a server fault: "that folder is already
	// covered by the main save" is a sentence, not a 500.
	if err := s.Daemon.Store.AddGameRoot(gameID, body.Name, body.Path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRemoveGameRoot(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	// Names travel in the path and can contain characters chi leaves encoded.
	name, err := url.PathUnescape(chi.URLParam(r, "root"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid location name")
		return
	}
	if err := s.Daemon.Store.RemoveGameRoot(gameID, name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.BroadcastGamesUpdate()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
