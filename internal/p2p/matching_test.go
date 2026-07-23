package p2p

import (
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

func newMatchTestEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatalf("store.Open error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("EnsureDefaultSettings error = %v", err)
	}
	return &Engine{Store: s, Log: func(string, string) {}}, s
}

// TestEnsureManifestGameMatching pins the peer-game resolution order:
// explicit alias and (opt-in) App ID both link a peer's differently-named
// game to the local canonical one, while the App ID path stays inert until
// the user turns it on — so a cracked and a legit copy never merge silently.
func TestEnsureManifestGameMatching(t *testing.T) {
	e, s := newMatchTestEngine(t)

	if err := s.CreateGame(store.Game{
		ID: "nevergrave", Name: "NeverGrave",
		SavePath: filepath.Join(t.TempDir(), "steam"), AppID: "2069710", ActiveBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}

	// Matching OFF (default): a peer game with the same App ID but a
	// different id must NOT resolve to the local game. With no name/path to
	// auto-track, it's simply reported not-found here.
	if _, err := e.ensureManifestGame("nevergrave-cracked", manifestGameQuery{AppID: "2069710"}); err == nil {
		t.Error("with App ID matching off, a shared App ID should not resolve to the local game")
	}

	// Turn App ID matching on.
	set, err := s.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.MatchByAppID = true
	if err := s.UpdateSettings(set); err != nil {
		t.Fatal(err)
	}

	// Now the same shared App ID resolves to the canonical game.
	g, err := e.ensureManifestGame("nevergrave-cracked", manifestGameQuery{AppID: "2069710"})
	if err != nil {
		t.Fatalf("ensureManifestGame (App ID on) error = %v", err)
	}
	if g.ID != "nevergrave" {
		t.Errorf("App ID match resolved to %q, want nevergrave", g.ID)
	}

	// An explicit link resolves regardless of App ID or the toggle.
	if err := s.AddGameAlias("some-portable-id", "nevergrave"); err != nil {
		t.Fatal(err)
	}
	g, err = e.ensureManifestGame("some-portable-id", manifestGameQuery{})
	if err != nil {
		t.Fatalf("ensureManifestGame (alias) error = %v", err)
	}
	if g.ID != "nevergrave" {
		t.Errorf("alias resolved to %q, want nevergrave", g.ID)
	}
}
