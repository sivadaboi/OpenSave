package api

import (
	"fmt"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/go-chi/chi/v5"
)

// handleLaunchGame starts a tracked game: a Steam AppID launches via the
// steam:// protocol handler; otherwise a configured executable path is run
// directly.
func (s *Server) handleLaunchGame(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	game, err := s.Daemon.Store.GetGame(gameID)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}

	switch {
	case game.AppID != "":
		if err := openURL("steam://run/" + game.AppID); err != nil {
			writeError(w, http.StatusInternalServerError, "could not launch via Steam: "+err.Error())
			return
		}
	case game.ExePath != "":
		if err := runExecutable(game.ExePath); err != nil {
			writeError(w, http.StatusInternalServerError, "could not launch executable: "+err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "no Steam App ID or executable configured for this game")
		return
	}

	s.Daemon.Log.Log("info", fmt.Sprintf("launched %q", game.Name))
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// openURL opens a URL/protocol handler with the OS default handler.
func openURL(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// runExecutable launches a program by path.
func runExecutable(path string) error {
	cmd := exec.Command(path)
	return cmd.Start()
}
