package e2e

import (
	"net/http"
	"testing"

	"github.com/opensave/opensave/testutil"
)

// Linking two differently-named copies of a game is the documented fallback
// for when App-ID matching can't help — a save under AppData\<company>\<game>
// carries no App ID anywhere in its path, so there is nothing to match on.
//
// The picker could only ever offer games from the local library, so the
// fallback did not exist for the case it was meant to cover: one entry per
// device, different names. This covers the missing half — asking a paired
// device what it tracks, and linking one of its entries to a local game.
func TestPeerLink_ListsPeerGamesAndLinksThem(t *testing.T) {
	a := testutil.NewTestDaemon(t, "PeerLink-A")
	b := testutil.NewTestDaemon(t, "PeerLink-B")
	a.PairWith(b)

	// The same game, tracked under a different name on each device.
	a.WriteSave("save.dat", "shared-save")
	b.WriteSave("save.dat", "shared-save")
	localID := a.TrackGame("Mina The Howler")

	var created struct {
		ID string `json:"id"`
	}
	b.API(http.MethodPost, "/api/games",
		map[string]string{"name": "Mina.The.Howler.Repack", "savePath": b.SaveDir}, &created)
	if created.ID == "" {
		t.Fatal("tracking the game on the peer returned no id")
	}

	// A can see what B tracks.
	var peerGames []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SavePath string `json:"savePath"`
	}
	a.API(http.MethodGet, "/api/peers/"+b.NodeID()+"/games", nil, &peerGames)

	var found bool
	for _, g := range peerGames {
		if g.ID == created.ID {
			found = true
			if g.Name != "Mina.The.Howler.Repack" {
				t.Errorf("peer game name = %q, want the name it is tracked under there", g.Name)
			}
			if g.SavePath == "" {
				t.Error("peer game has no save path — the picker can't tell duplicates apart without one")
			}
		}
	}
	if !found {
		t.Fatalf("the peer's game is missing from its list: %+v", peerGames)
	}

	// Linking it must not disturb the local library: the peer's entry is not
	// a local game, so there is nothing here to merge or remove.
	a.API(http.MethodPost, "/api/games/"+localID+"/link", map[string]string{"alias": created.ID}, nil)

	// /api/games is keyed by game id, not a list.
	localGames := map[string]struct {
		Name string `json:"name"`
	}{}
	a.API(http.MethodGet, "/api/games", nil, &localGames)
	if _, ok := localGames[localID]; !ok {
		t.Fatalf("linking a peer's game removed the local game it was linked to: %+v", localGames)
	}

	// And the link is recorded, so the peer's id resolves here.
	var aliases []struct {
		ID string `json:"id"`
	}
	a.API(http.MethodGet, "/api/games/"+localID+"/aliases", nil, &aliases)
	var linked bool
	for _, al := range aliases {
		if al.ID == created.ID {
			linked = true
		}
	}
	if !linked {
		t.Errorf("the peer's game id was not recorded as an alias: %+v", aliases)
	}
}

// The picker asks every paired device and shows whichever answer. An
// unreachable one must fail on its own rather than taking the request down,
// or a single offline device would hide every other device's games.
func TestPeerLink_UnreachablePeerFailsAlone(t *testing.T) {
	a := testutil.NewTestDaemon(t, "PeerLinkOffline-A")
	b := testutil.NewTestDaemon(t, "PeerLinkOffline-B")
	a.PairWith(b)

	b.Server.Stop() // B is gone; A still holds the pairing

	status := a.APIStatus(http.MethodGet, "/api/peers/"+b.NodeID()+"/games", nil, nil)
	if status == http.StatusOK {
		t.Errorf("a dead peer reported success (%d) — the picker would show an empty list as if it had no games", status)
	}
}
