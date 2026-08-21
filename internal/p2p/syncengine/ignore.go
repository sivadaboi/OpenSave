package syncengine

import (
	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/ignore"
	"github.com/opensave/opensave/internal/store"
)

// Applying a game's exclusion rules.
//
// The rules are applied HERE, at the decision, and not by filtering the
// manifest when it is built. That distinction is the whole safety argument.
//
// Compute decides what a missing file means by looking at the lineage: a file
// the peer has and this device does not is a deletion to propagate if the two
// once shared it, and a file to pull if not. Hide an excluded file from the
// local manifest alone and both answers are wrong — either this device tells
// the peer to DELETE the config it was trying to protect, or it pulls the
// peer's copy and the exclusion achieves nothing.
//
// So an excluded path is removed from both sides' manifests AND from the
// lineage, before any of that reasoning runs. The path then does not exist as
// far as the decision is concerned: never pulled, never pushed, never deleted,
// on either side.
//
// The rules are not sent to the peer, and the manifest this device serves is
// NOT filtered. A peer that has no rule of its own must keep seeing the file
// listed, or it would read the gap as a deletion and remove its own copy —
// the exact harm, inflicted on the other machine. It may still offer the file;
// this side simply never accepts it. A device with the rule is protected
// whatever the other one does, and a device without it is no worse off than
// before the feature existed.

// rulesFor returns a game's compiled exclusion rules.
func (e *Engine) rulesFor(gameID string) ignore.Rules {
	game, err := e.Store.GetGame(gameID)
	if err != nil {
		return ignore.Rules{}
	}
	return ignore.Parse(game.SyncIgnore)
}

// filterManifest returns a copy with every excluded path removed. The
// original is untouched: it is still what gets served to peers.
func filterManifest(m delta.Manifest, rules ignore.Rules) delta.Manifest {
	if rules.Empty() {
		return m
	}
	out := delta.Manifest{
		Timestamp: m.Timestamp,
		Files:     make(map[string]delta.FileEntry, len(m.Files)),
	}
	// LatestMtime is recomputed from what survives, not carried over.
	//
	// It is the "has this side changed recently" signal the conflict detector
	// falls back on when there is no merge base. Keep the excluded file's
	// timestamp and editing a config nobody syncs still makes this device look
	// freshly modified — so two people each editing their own machine's config
	// would be told their saves diverged, over a file neither of them is
	// syncing.
	for p, entry := range m.Files {
		if rules.Match(p) {
			continue
		}
		out.Files[p] = entry
		if entry.MtimeMs > out.LatestMtime {
			out.LatestMtime = entry.MtimeMs
		}
	}
	for _, d := range m.Dirs {
		if rules.Match(d) {
			continue
		}
		out.Dirs = append(out.Dirs, d)
	}
	// Extra save locations keep their own contents; the same rules apply
	// within each, since a rule names a file inside a folder rather than
	// naming which folder it is in.
	if len(m.Extra) > 0 {
		out.Extra = make(map[string]delta.RootManifest, len(m.Extra))
		for name, root := range m.Extra {
			sub := delta.RootManifest{Files: make(map[string]delta.FileEntry, len(root.Files))}
			for p, entry := range root.Files {
				if rules.Match(p) {
					continue
				}
				sub.Files[p] = entry
			}
			for _, d := range root.Dirs {
				if rules.Match(d) {
					continue
				}
				sub.Dirs = append(sub.Dirs, d)
			}
			out.Extra[name] = sub
		}
	}
	return out
}

// filterLineage drops excluded paths from the recorded shared state.
//
// Without this the exclusion is worse than useless on a game that synced
// before the rule was written: the path is still in the lineage, so the
// decision reads "we both had this and now I don't" and propagates a deletion
// to the peer.
func filterLineage(paths map[string]struct{}, rules ignore.Rules) map[string]struct{} {
	if rules.Empty() {
		return paths
	}
	out := make(map[string]struct{}, len(paths))
	for p := range paths {
		if rules.Match(p) {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

// filterPathList drops excluded paths from a plain list, for the lineage that
// is persisted back after a sync.
func filterPathList(paths []string, rules ignore.Rules) []string {
	if rules.Empty() {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if rules.Match(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// FilteredContentHash is the merge base a game would have right now under a
// given set of rules — its save as the sync engine sees it.
//
// Used when a game's exclusion rules change: the two devices agreed a moment
// before, and removing files from consideration leaves them agreeing on what
// remains, so this value can be written as the new base without claiming
// anything that was not already true.
func (e *Engine) FilteredContentHash(gameID string, game store.Game) string {
	m, err := delta.BuildManifest(game.SavePath)
	if err != nil {
		return ""
	}
	return filterManifest(m, ignore.Parse(game.SyncIgnore)).ManifestHash()
}
