package store

import (
	"errors"
	"testing"
)

func TestFindGameByAppID(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "nevergrave", Name: "NeverGrave", SavePath: `H:\Steam\NeverGrave`, AppID: "2069710"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(Game{ID: "other", Name: "Other", SavePath: `H:\Steam\Other`}); err != nil {
		t.Fatal(err)
	}

	got, err := s.FindGameByAppID("2069710")
	if err != nil {
		t.Fatalf("FindGameByAppID error = %v", err)
	}
	if got.ID != "nevergrave" {
		t.Errorf("FindGameByAppID = %q, want nevergrave", got.ID)
	}

	if _, err := s.FindGameByAppID("0000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown appid err = %v, want ErrNotFound", err)
	}
	if _, err := s.FindGameByAppID(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty appid err = %v, want ErrNotFound", err)
	}
}

func TestGameAliasCRUDAndCascade(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "nevergrave", Name: "NeverGrave", SavePath: `H:\Steam\NeverGrave`}); err != nil {
		t.Fatal(err)
	}

	if err := s.AddGameAlias("nevergrave-portable", "nevergrave"); err != nil {
		t.Fatalf("AddGameAlias error = %v", err)
	}
	// Re-adding the same alias updates rather than errors (upsert).
	if err := s.AddGameAlias("nevergrave-portable", "nevergrave"); err != nil {
		t.Fatalf("re-AddGameAlias error = %v", err)
	}

	if got, ok := s.ResolveGameAlias("nevergrave-portable"); !ok || got != "nevergrave" {
		t.Errorf("ResolveGameAlias = (%q,%v), want (nevergrave,true)", got, ok)
	}
	if _, ok := s.ResolveGameAlias("unknown"); ok {
		t.Errorf("ResolveGameAlias(unknown) should be false")
	}

	aliases, err := s.ListGameAliases("nevergrave")
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || aliases[0] != "nevergrave-portable" {
		t.Errorf("ListGameAliases = %v, want [nevergrave-portable]", aliases)
	}

	// Invalid links are rejected.
	if err := s.AddGameAlias("x", "x"); err == nil {
		t.Error("self-alias should be rejected")
	}
	if err := s.AddGameAlias("", "nevergrave"); err == nil {
		t.Error("empty alias should be rejected")
	}

	// Deleting the canonical game cascades its aliases away.
	if err := s.DeleteGame("nevergrave"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ResolveGameAlias("nevergrave-portable"); ok {
		t.Error("alias should be gone after canonical game deleted (ON DELETE CASCADE)")
	}
}

func TestRemoveGameAlias(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "a", Name: "A", SavePath: `C:\A`}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGameAlias("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveGameAlias("b"); err != nil {
		t.Fatalf("RemoveGameAlias error = %v", err)
	}
	if _, ok := s.ResolveGameAlias("b"); ok {
		t.Error("alias should be gone after RemoveGameAlias")
	}
}
