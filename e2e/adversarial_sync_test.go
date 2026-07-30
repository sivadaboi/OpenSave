package e2e

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// A snapshot keeps filenames inside a zip; a sync puts them on the wire as
// JSON, base64 and URL path segments. Those are different code paths with
// different ways to mangle a name, so surviving one proves nothing about the
// other — and a mangled name means a save file that silently does not arrive.
func TestAdversarialSync_AwkwardFilenamesCrossTheWire(t *testing.T) {
	files := map[string]string{
		"plain.sav":               "plain",
		"with spaces.sav":         "spaces",
		"unicode-日本語.sav":         "japanese",
		"emoji-🎮.sav":             "emoji",
		"accented-café.sav":       "accents",
		"parens(1).sav":           "parens",
		"hash#and%percent.sav":    "urlish",
		"plus+sign.sav":           "plus",
		"amp&ersand.sav":          "ampersand",
		"deep/nested/path/x.sav":  "nested",
		"dots.in.name.sav":        "dots",
	}

	a, b, gameID := pairAndTrack(t, "WireNames", files)

	// pairAndTrack already waited for every file to arrive, so reaching here
	// means they transferred. Confirm the content matches byte for byte —
	// arriving with the wrong bytes is worse than not arriving.
	for rel, want := range files {
		if got := b.ReadSave(rel); got != want {
			t.Errorf("%q arrived as %q, want %q", rel, got, want)
		}
	}

	// And that an edit to an awkwardly named file propagates, which is the
	// delta path rather than the initial transfer.
	time.Sleep(syncSettleWindow)
	a.WriteSave("unicode-日本語.sav", "edited-japanese")
	a.WriteSave("emoji-🎮.sav", "edited-emoji")
	syncTo(a, gameID, b.NodeID())

	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("unicode-日本語.sav") == "edited-japanese" &&
			b.ReadSave("emoji-🎮.sav") == "edited-emoji"
	}) {
		t.Errorf("edits to non-ASCII filenames did not propagate: %q / %q",
			b.ReadSave("unicode-日本語.sav"), b.ReadSave("emoji-🎮.sav"))
	}
}

// Zero-byte files have no blocks to transfer, which is exactly the case a
// block-based protocol can quietly skip.
func TestAdversarialSync_EmptyFilesTransfer(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "WireEmpty", map[string]string{"seed.sav": "seed"})

	time.Sleep(syncSettleWindow)
	a.WriteSave("empty.sav", "")
	a.WriteSave("nested/empty.dat", "")
	syncTo(a, gameID, b.NodeID())

	if !testutil.WaitFor(45*time.Second, func() bool {
		for _, rel := range []string{"empty.sav", "nested/empty.dat"} {
			info, err := os.Stat(filepath.Join(b.SaveDir, filepath.FromSlash(rel)))
			if err != nil || info.Size() != 0 {
				return false
			}
		}
		return true
	}) {
		t.Error("empty files never arrived on the peer — a block-based transfer skipped what has no blocks")
	}
}

// Two syncs of the same game at once must not interleave into a corrupted
// save. The engine has a guard for this; a save tool is the wrong place to
// find out it does not hold.
func TestAdversarialSync_ConcurrentSyncsOfOneGame(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Concurrent", map[string]string{
		"slot1.sav": "start",
		"slot2.sav": "start",
	})

	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "concurrent-edit")
	a.WriteSave("slot2.sav", "concurrent-edit")

	// Fire several syncs at once and let them fight.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Status is not asserted: a losing caller is told the sync is
			// already running, which is correct. What matters is the result.
			a.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
		}()
	}
	wg.Wait()

	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "concurrent-edit" &&
			b.ReadSave("slot2.sav") == "concurrent-edit"
	}) {
		t.Errorf("after concurrent syncs the peer holds slot1=%q slot2=%q, want both %q",
			b.ReadSave("slot1.sav"), b.ReadSave("slot2.sav"), "concurrent-edit")
	}

	// The originating device must be untouched by its own sync.
	if got := a.ReadSave("slot1.sav"); got != "concurrent-edit" {
		t.Errorf("the sending device's own save changed under it: %q", got)
	}
}

// A peer that vanishes mid-conversation must leave the local save alone. The
// failure has to be a failed sync, not a half-written save folder.
func TestAdversarialSync_PeerDisappearsMidSync(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "PeerVanish", map[string]string{"slot1.sav": "before"})

	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "local-edit")

	b.Server.Stop() // the peer is gone

	// Should fail or report the peer unreachable; must not hang or panic.
	a.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	if got := a.ReadSave("slot1.sav"); got != "local-edit" {
		t.Errorf("slot1.sav = %q — syncing with a dead peer modified the local save", got)
	}

	// And the daemon is still healthy afterwards.
	var status map[string]any
	a.API(http.MethodGet, "/api/status", nil, &status)
	if status == nil {
		t.Error("the daemon stopped answering after a peer disappeared mid-sync")
	}
}

// A file replaced by a directory (and the reverse) between syncs is rare but
// real — repacks and save editors do it. Neither side may end up with a
// half-applied tree.
func TestAdversarialSync_FileReplacedByDirectory(t *testing.T) {
	a, _, gameID := pairAndTrack(t, "TypeSwap", map[string]string{"thing.sav": "i am a file"})

	time.Sleep(syncSettleWindow)
	p := filepath.Join(a.SaveDir, "thing.sav")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	a.WriteSave("thing.sav/inner.dat", "now i am a folder")

	// Must not panic or wedge; the outcome may legitimately be a conflict.
	a.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)

	var status map[string]any
	a.API(http.MethodGet, "/api/status", nil, &status)
	if status == nil {
		t.Fatal("the daemon stopped answering after a file/directory type swap")
	}
	// The local tree must still be what we made it.
	if got := a.ReadSave("thing.sav/inner.dat"); got != "now i am a folder" {
		t.Errorf("the local tree was altered by the sync attempt: %q", got)
	}
}

// A save that grows large enough to cross the block-size thresholds exercises
// the chunking boundaries, where an off-by-one drops or duplicates a block.
func TestAdversarialSync_LargeFileAcrossBlockBoundaries(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "BlockEdges", map[string]string{"seed.sav": "seed"})

	// Sizes chosen to straddle the 64 KB default block size.
	for _, size := range []int{65535, 65536, 65537, 131072 + 1} {
		content := strings.Repeat("A", size)
		rel := fmt.Sprintf("blob_%d.bin", size)
		time.Sleep(syncSettleWindow)
		a.WriteSave(rel, content)
		syncTo(a, gameID, b.NodeID())

		if !testutil.WaitFor(60*time.Second, func() bool {
			return len(b.ReadSave(rel)) == size
		}) {
			t.Errorf("%s arrived with %d bytes, want %d", rel, len(b.ReadSave(rel)), size)
			continue
		}
		if b.ReadSave(rel) != content {
			t.Errorf("%s arrived with the right length but different bytes", rel)
		}
	}
}
