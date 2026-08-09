package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/opensave/opensave/internal/ignore"
)

// Listing what is actually in a game's save folders, and whether each file
// currently syncs.
//
// The exclusion rules were typed into a box with nothing to type against: you
// had to already know the filename, for a folder you could not see, in a
// syntax you had to learn, and the first feedback was a file turning up on
// another machine days later. This is the other half — the same list the rules
// are matched against, with the verdict shown per file.
//
// The verdict is computed with the SAME matcher the sync engine uses, on the
// same relative paths, so the answer here is the answer there. Reimplementing
// the matching for display would be a second nearly-right copy of the one
// thing this feature has to get exactly right.

// saveFile is one file inside one of a game's save locations.
type saveFile struct {
	// Path relative to the location's root, forward-slashed — exactly what
	// the rules match against.
	Path string `json:"path"`
	// Which location it is in: "" for the game's own save folder, otherwise
	// the location's name.
	Location string `json:"location"`
	SizeBytes int64  `json:"sizeBytes"`
	MtimeMs   int64  `json:"mtimeMs"`
	// Excluded reports whether the current rules stop this file syncing.
	Excluded bool `json:"excluded"`
}

const (
	// saveFileListCap bounds the response. A save folder with more files than
	// this is not something anyone is going to pick through by eye, and the
	// pattern box is still there for it.
	saveFileListCap = 3000
)

// handleGameSaveFiles lists every file across a game's save locations, each
// marked with whether the game's current exclusion rules let it sync.
//
// Accepts an optional ?rules= override so the client can preview a list it has
// not saved yet: the point of showing the verdict is watching it change as you
// type, and requiring a save first would mean writing a rule to find out
// whether it was the rule you meant.
func (s *Server) handleGameSaveFiles(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameId")
	game, err := s.Daemon.Store.GetGame(gameID)
	if err != nil {
		writeError(w, notFoundToStatus(err), err.Error())
		return
	}

	text := game.SyncIgnore
	if raw, ok := r.URL.Query()["rules"]; ok {
		text = raw[0] // present-but-empty means "no rules", not "use the saved ones"
	}
	rules := ignore.Parse(text)

	roots := []struct{ name, path string }{{"", game.SavePath}}
	if extra, rootsErr := s.Daemon.Store.ListGameRoots(gameID); rootsErr == nil {
		for _, e := range extra {
			if strings.TrimSpace(e.Path) != "" {
				roots = append(roots, struct{ name, path string }{e.Name, e.Path})
			}
		}
	}

	out := []saveFile{}
	truncated := false
	for _, root := range roots {
		files, cut := listSaveFiles(root.path, saveFileListCap-len(out))
		truncated = truncated || cut
		for _, f := range files {
			f.Location = root.name
			f.Excluded = rules.Match(f.Path)
			out = append(out, f)
		}
		if len(out) >= saveFileListCap {
			truncated = true
			break
		}
	}

	// Excluded first, then by path: the reason for opening this list is
	// usually to check what a rule is catching, and hunting for the few
	// matches among hundreds of rows is the thing being avoided.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Excluded != out[j].Excluded {
			return out[i].Excluded
		}
		if out[i].Location != out[j].Location {
			return out[i].Location < out[j].Location
		}
		return out[i].Path < out[j].Path
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"files":     out,
		"truncated": truncated,
	})
}

// listSaveFiles walks one location, returning at most limit files and whether
// it stopped early. A location that cannot be read yields nothing rather than
// an error: one unreadable folder must not blank the whole list.
func listSaveFiles(root string, limit int) ([]saveFile, bool) {
	if limit <= 0 {
		return nil, true
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, false
	}
	// A save that is a single file is listed under its own name, which is also
	// what a rule naming it would match.
	if !info.IsDir() {
		return []saveFile{{
			Path:      filepath.Base(root),
			SizeBytes: info.Size(),
			MtimeMs:   info.ModTime().UnixMilli(),
		}}, false
	}

	var out []saveFile
	truncated := false
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if len(out) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		out = append(out, saveFile{
			Path:      filepath.ToSlash(rel),
			SizeBytes: fi.Size(),
			MtimeMs:   fi.ModTime().UnixMilli(),
		})
		return nil
	})
	if err != nil {
		return out, truncated
	}
	return out, truncated
}
