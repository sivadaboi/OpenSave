package p2p

import (
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// The rule that keeps files where they belong: a root name this device does
// not have is refused outright.
//
// The tempting alternative — fall back to the primary location — is how a
// peer asking for "config/settings.ini" ends up reading, or deleting, a file
// inside the save folder. The whole reason locations are named is so that a
// request about one can never be answered with another.
func TestUnknownRootIsRefusedRatherThanServedFromTheSaveFolder(t *testing.T) {
	e, s := newMatchTestEngine(t)
	game := store.Game{ID: "elden-ring", Name: "Elden Ring", SavePath: `C:\Saves\ER`}
	if err := s.CreateGame(game); err != nil {
		t.Fatal(err)
	}

	// The primary location resolves to the game's save path, as always.
	if path, ok := e.resolveServeRoot(game.ID, game, ""); !ok || path != game.SavePath {
		t.Errorf("primary root resolved to (%q, %v), want (%q, true)", path, ok, game.SavePath)
	}

	// A name this device has never heard of must not resolve at all.
	path, ok := e.resolveServeRoot(game.ID, game, "config")
	if ok {
		t.Errorf("an unknown location resolved to %q; a peer could read or delete inside the save folder by naming a location this device lacks", path)
	}

	// Known but unmapped is still not a location that can be served: there is
	// no path to serve from, and defaulting would land in the save folder.
	if err := s.AddGameRoot(game.ID, "config", ""); err != nil {
		t.Fatal(err)
	}
	if path, ok := e.resolveServeRoot(game.ID, game, "config"); ok {
		t.Errorf("a location known but not mapped here resolved to %q, want refusal", path)
	}

	// Once mapped, it resolves to its own path — never the save folder's.
	if err := s.AddGameRoot(game.ID, "config", `C:\Docs\ER`); err != nil {
		t.Fatal(err)
	}
	path, ok = e.resolveServeRoot(game.ID, game, "config")
	if !ok || path != `C:\Docs\ER` {
		t.Errorf("mapped location resolved to (%q, %v), want (C:\\Docs\\ER, true)", path, ok)
	}
	if path == game.SavePath {
		t.Error("the extra location resolved to the primary save path")
	}
}
