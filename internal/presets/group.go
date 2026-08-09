package presets

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Grouping collapses the several rows a single game usually produces into one.
//
// A scan finds the same game in more than one place as a matter of course: it
// looks for saves by half a dozen conventions at once, and a game that matches
// two of them appears twice. On the library this was built against, 117 of 235
// results were a second row for a game already in the list.
//
// What those extra rows mean is not one thing, and that is the whole
// difficulty. They fall into three kinds, and the right answer differs for
// each:
//
//   - One folder sits INSIDE another. "SonsOfTheForest" and
//     "SonsOfTheForest/Saves" are the same files seen twice. Tracking both is
//     not just redundant, it is refused — two locations covering the same
//     files fight over them — so the inner one is marked and set aside.
//
//   - Several folders sit BESIDE each other under one parent. TrackMania keeps
//     Scores, Tracks and Profiles as siblings, and all three are the game's
//     data. This is the split save that extra locations exist for, so they are
//     offered together as one game.
//
//   - Folders in unrelated places. The same game played through different
//     launchers, or a folder left behind by an install that has moved on.
//     Usually exactly one is live. They are offered as alternatives with the
//     freshest picked, because picking for someone is only safe when the thing
//     picked is visibly the thing they would have picked.
//
// A real group is often a mixture of all three, so the kind is decided per
// entry against the group rather than once for the group as a whole.
const (
	// RoleOnly is a game found in exactly one place.
	RoleOnly = "only"
	// RolePrimary is the folder to track as the game itself.
	RolePrimary = "primary"
	// RoleLocation is a folder beside the primary holding another part of the
	// same save — an extra location, suggested along with it.
	RoleLocation = "location"
	// RoleInside is a folder within another of the group's folders. Already
	// covered, and not separately trackable.
	RoleInside = "inside"
	// RoleAlternative is a folder somewhere unrelated: the same game found by
	// another route, most often one that is no longer played. Offered, not
	// suggested.
	RoleAlternative = "alternative"
)

// nameQualifier strips a trailing "(…)" — Proton and Unreal entries carry one
// to tell a prefix's candidates apart, and it is exactly what must be ignored
// when deciding whether two rows are the same game.
var nameQualifier = regexp.MustCompile(`\s*\([^()]*\)\s*$`)

// groupNameKey reduces a display name to something two rows for one game will
// agree on. normalizeGameName already handles the spellings a save folder
// arrives under — repack separators, dropped apostrophes, trademark marks —
// so this only has to drop the qualifier first and close up the spaces it
// leaves behind.
func groupNameKey(name string) string {
	n := nameQualifier.ReplaceAllString(name, "")
	return strings.ReplaceAll(normalizeGameName(n), " ", "")
}

// genericNames are folder-derived names that say nothing about which game a
// folder belongs to. Two rows both called "saves" are not evidence of one
// game, and grouping on that would merge unrelated titles — the one mistake
// here that puts someone's save somewhere they did not choose.
var genericNames = map[string]bool{
	"save": true, "saves": true, "savegame": true, "savegames": true,
	"savedata": true, "saved": true, "data": true, "game": true, "games": true,
	"profile": true, "profiles": true, "user": true, "users": true,
	"userdata": true, "config": true, "settings": true, "backup": true,
	"backups": true, "remote": true, "storage": true, "local": true,
}

// groupKey is what two rows must share to be treated as one game. An AppID is
// authoritative; without one the normalized name has to do, and a name that
// could belong to anything is refused a group of its own.
func groupKey(d DiscoveredSave) string {
	if d.AppID != "" {
		return "app:" + d.AppID
	}
	n := groupNameKey(d.Name)
	if n == "" || genericNames[n] {
		return "" // ungroupable: stands alone
	}
	return "name:" + n
}

// Group assigns every save a group id and a role within that group, in place.
// Call it after Measure: the roles depend on which folders hold anything and
// when they were last written.
func Group(saves []DiscoveredSave) {
	buckets := map[string][]int{}
	var order []string
	for i := range saves {
		key := groupKey(saves[i])
		if key == "" {
			// Its own group, named for the entry, so the client can treat
			// every row the same way.
			saves[i].GroupID = "id:" + saves[i].ID
			saves[i].Role = RoleOnly
			continue
		}
		if _, seen := buckets[key]; !seen {
			order = append(order, key)
		}
		buckets[key] = append(buckets[key], i)
	}

	for _, key := range order {
		idxs := buckets[key]
		for _, i := range idxs {
			saves[i].GroupID = key
		}
		if len(idxs) == 1 {
			saves[idxs[0]].Role = RoleOnly
			continue
		}
		assignRoles(saves, idxs)
	}
}

// assignRoles works out what each folder in a multi-folder group is.
func assignRoles(saves []DiscoveredSave, idxs []int) {
	// Folders contained by another of the group's folders. Checked both ways
	// round so a chain (a > b > c) marks b and c, not just b.
	inside := make(map[int]bool, len(idxs))
	for _, outer := range idxs {
		for _, inner := range idxs {
			if outer == inner {
				continue
			}
			if pathWithin(saves[inner].SavePath, saves[outer].SavePath) {
				inside[inner] = true
			}
		}
	}

	candidates := make([]int, 0, len(idxs))
	for _, i := range idxs {
		if !inside[i] {
			candidates = append(candidates, i)
		}
	}
	// Every folder inside another can only happen if two paths are equal, and
	// the scanner deduplicates those. Guarded anyway: a group with no primary
	// would offer nothing to track.
	if len(candidates) == 0 {
		candidates = idxs
	}

	primary := pickPrimary(saves, candidates)
	primaryParent := parentDir(saves[primary].SavePath)

	for _, i := range idxs {
		switch {
		case i == primary:
			saves[i].Role = RolePrimary
		case inside[i]:
			saves[i].Role = RoleInside
		case parentDir(saves[i].SavePath) == primaryParent:
			// Beside the primary, under the same parent: the split-save shape.
			saves[i].Role = RoleLocation
		default:
			saves[i].Role = RoleAlternative
		}
	}
}

// pickPrimary chooses the folder to offer as the game itself: the one that
// looks most like the save being played. A folder holding files beats one
// holding none, then the most recently written, then the largest — and path
// order last, so the result never depends on the order the scanner happened
// to find things in.
func pickPrimary(saves []DiscoveredSave, candidates []int) int {
	best := candidates[0]
	for _, i := range candidates[1:] {
		if primaryBetter(saves[i], saves[best]) {
			best = i
		}
	}
	return best
}

func primaryBetter(a, b DiscoveredSave) bool {
	// A folder known to be empty always loses to one that is not; a folder
	// that could not be measured is not known to be empty, so it does not.
	if a.IsEmpty() != b.IsEmpty() {
		return b.IsEmpty()
	}
	if a.LatestMtime != b.LatestMtime {
		return a.LatestMtime > b.LatestMtime
	}
	if a.FileCount != b.FileCount {
		return a.FileCount > b.FileCount
	}
	return a.SavePath < b.SavePath
}

// pathWithin reports whether inner sits below outer. Compared case-insensitively
// because Windows paths are, and a scan that mixed cases would otherwise miss
// the containment and offer both.
func pathWithin(inner, outer string) bool {
	i, o := cleanForMatch(inner), cleanForMatch(outer)
	if i == "" || o == "" || i == o {
		return false
	}
	return strings.HasPrefix(i, o+string(filepath.Separator))
}

// parentDir is the containing folder, normalized for comparison.
func parentDir(p string) string {
	c := cleanForMatch(p)
	if c == "" {
		return ""
	}
	return filepath.Dir(c)
}

// Groups splits saves into their groups, primary first within each, and
// ordered by where each group's primary appeared in the input so a listing
// keeps the scanner's ordering. Group must have been called first.
func Groups(saves []DiscoveredSave) [][]DiscoveredSave {
	index := map[string]int{}
	var out [][]DiscoveredSave
	for _, s := range saves {
		at, seen := index[s.GroupID]
		if !seen {
			index[s.GroupID] = len(out)
			out = append(out, []DiscoveredSave{s})
			continue
		}
		out[at] = append(out[at], s)
	}
	for _, g := range out {
		sort.SliceStable(g, func(i, j int) bool { return roleRank(g[i].Role) < roleRank(g[j].Role) })
	}
	return out
}

// roleRank orders a group's folders for display: the one to track, then the
// ones suggested with it, then the ones merely offered.
func roleRank(role string) int {
	switch role {
	case RoleOnly, RolePrimary:
		return 0
	case RoleLocation:
		return 1
	case RoleAlternative:
		return 2
	default: // RoleInside
		return 3
	}
}
