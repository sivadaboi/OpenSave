package syncengine

import "github.com/opensave/opensave/internal/delta"

// filesAtRisk lists the local files this sync is about to destroy: the ones a
// pull will write over, and the ones a peer's deletion will remove.
//
// The distinction matters more than it looks. A pull that only brings files
// this device has never held destroys nothing — there is no local version to
// lose — so it needs neither a safety snapshot nor a conflict prompt. Judging
// risk by "is anything being pulled" instead of "is anything being replaced"
// treats those two cases the same, and that is what made a sync refuse to
// deliver a brand-new file because an unrelated folder had been edited.
func filesAtRisk(local delta.Manifest, d Decision) []string {
	if len(local.Files) == 0 {
		return nil
	}
	var out []string
	for _, p := range d.FilesToPull {
		if _, held := local.Files[p]; held {
			out = append(out, p)
		}
	}
	// A deletion arriving from the peer removes the local copy just as
	// thoroughly as an overwrite does, and is easier to be wrong about: the
	// file is gone rather than replaced with something recognisable.
	for _, p := range d.FilesToDeleteLocally {
		if _, held := local.Files[p]; held {
			out = append(out, p)
		}
	}
	return out
}
