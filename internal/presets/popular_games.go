package presets

import (
	"strings"
	"sync"
)

// popularSteamGames is the offline fallback dictionary of well-known
// Steam AppIDs, resolving names instantly without hitting the Store API.
var popularSteamGames = map[string]string{
	"480":     "Spacewar (Steam Overlay Wrapper)",
	"730":     "Counter-Strike 2",
	"570":     "Dota 2",
	"550":     "Left 4 Dead 2",
	"400":     "Portal",
	"620":     "Portal 2",
	"105600":  "Terraria",
	"292030":  "The Witcher 3: Wild Hunt",
	"271590":  "Grand Theft Auto V",
	"1091500": "Cyberpunk 2077",
	"1174180": "Red Dead Redemption 2",
	"1245620": "Elden Ring",
	"377160":  "Fallout 4",
	"413150":  "Stardew Valley",
	"814380":  "Sekiro: Shadows Die Twice",
	"1151640": "Horizon Zero Dawn",
	"218620":  "Payday 2",
	"252490":  "Rust",
	"381210":  "Dead by Daylight",
	"578080":  "PUBG: BATTLEGROUNDS",
	"108600":  "Project Zomboid",
	"230410":  "Warframe",
	"311210":  "Call of Duty: Black Ops III",
	"1145360": "Hades",
	"1145350": "Hades II",
	"268910":  "Cuphead",
	"219740":  "Don't Starve",
	"322330":  "Don't Starve Together",
	"250900":  "The Binding of Isaac: Rebirth",
	"1817070": "Marvel's Spider-Man Remastered",
	"1817190": "Marvel's Spider-Man: Miles Morales",
	"1551360": "Forza Horizon 5",
	"236390":  "War Thunder",
	"2050650": "Resident Evil 4",
	"1190460": "Death Stranding",
	"289070":  "Sid Meier's Civilization VI",
	"646570":  "Slay the Spire",
	"4000":    "Garry's Mod",
	"2280":    "Doom",
	"379720":  "DOOM (2016)",
	"782330":  "DOOM Eternal",
	"2358720": "Black Myth: Wukong",
	"461040":  "PICO PARK:Classic Edition",
	"1509960": "PICO PARK",
	"2644470": "PICO PARK 2",
	"367520":  "Hollow Knight",
	"264710":  "Subnautica",
	"1326470": "Sons Of The Forest",
	"242760":  "The Forest",
	"1229490": "ULTRAKILL",
	"813230":  "ANIMAL WELL",
	"3241660": "R.E.P.O.",
	"1533420": "Lethal Company",
}

// extraNameAliases are additional lowercase-name -> AppID matches beyond
// the exact popular-game titles (short forms users actually name folders).
var extraNameAliases = map[string]string{
	"elden ring":            "1245620",
	"cyberpunk 2077":        "1091500",
	"the witcher 3":         "292030",
	"witcher 3":             "292030",
	"hades":                 "1145360",
	"hades ii":              "1145350",
	"hades 2":               "1145350",
	"terraria":              "105600",
	"sekiro":                "814380",
	"stardew valley":        "413150",
	"fallout 4":             "377160",
	"red dead redemption 2": "1174180",
	"black myth":            "2358720",
	"wukong":                "2358720",
	"b1":                    "2358720", // Black Myth: Wukong's UE project codename
	"pico park":             "1509960",
	"hollow knight":         "367520",
}

// nameToAppIDIndex builds the lowercase-name -> AppID lookup used to infer
// AppIDs for discoveries that only have a folder name.
func nameToAppIDIndex() map[string]string {
	index := make(map[string]string, len(popularSteamGames)+len(extraNameAliases))
	for appID, name := range popularSteamGames {
		index[strings.ToLower(name)] = appID
	}
	for name, appID := range extraNameAliases {
		index[name] = appID
	}
	return index
}

// normalizeGameName reduces a name to a comparison key, so the spellings the
// same title arrives under all land on one value.
//
// A save folder rarely carries the store's exact punctuation. Repack folders
// use separators instead of spaces ("Mina.The.Howler"), apostrophes are
// dropped as often as they are kept ("Baldurs Gate 3"), and trademark marks
// come and go. None of those are different games, and comparing raw strings
// treats every one of them as unknown.
func normalizeGameName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true // leading separators collapse away
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastSpace = false
		case r == ' ' || r == '.' || r == '_' || r == '-' || r == ':' || r == '+':
			// Separators, not content: collapse runs to one space.
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			// Apostrophes, ™, ®, and anything else: drop without joining
			// words, so "baldur's" and "baldurs" agree.
		}
	}
	return strings.TrimSpace(b.String())
}

// inferAppIDFromName resolves a discovered folder/game name to a Steam App ID.
//
// Order is by confidence, not convenience:
//
//  1. exact match in the curated list, which is hand-maintained;
//  2. exact match in the Ludusavi manifest, tens of thousands of titles and
//     the reason a game outside the curated list can be recognised at all;
//  3. substring containment, curated list only.
//
// Containment stays off the manifest deliberately. Over a handful of curated
// names it usefully matches "EldenRing Backup" to "elden ring"; over tens of
// thousands it starts claiming that "Portal" is "Portal Knights" and that
// every folder called "Data" is something. An exact hit on the manifest is
// evidence; a substring hit across all of it is a coin toss, and this feeds
// App-ID matching, which decides whether two devices' saves are the same
// game.
func inferAppIDFromName(name string, index map[string]string) string {
	// Strip parenthesized suffixes like "(Epic/Unreal Save)".
	key := strings.ToLower(name)
	if i := strings.IndexByte(key, '('); i >= 0 {
		key = key[:i]
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}

	if appID, ok := index[key]; ok {
		return appID
	}

	norm := normalizeGameName(key)
	if norm != "" {
		if appID, ok := index[norm]; ok {
			return appID
		}
		if appID, ok := manifestNameIndex()[norm]; ok {
			return appID
		}
		// Folder names lose their word boundaries all the time —
		// "MinaTheHollower", "ELDENRING", "hollowknight". Comparing with the
		// spaces taken out of both sides catches those without loosening
		// anything else: this is still an exact match, just of a string that
		// has had one kind of noise removed.
		if compact := strings.ReplaceAll(norm, " ", ""); compact != norm {
			if appID, ok := manifestCompactIndex()[compact]; ok {
				return appID
			}
		} else if appID, ok := manifestCompactIndex()[norm]; ok {
			// The discovery has no spaces to begin with, so it can only match
			// a manifest name whose spaces were removed.
			return appID
		}
	}

	for indexName, appID := range index {
		// Containment only for reasonably long keys — short aliases like
		// "b1" (Wukong's codename) must only ever match exactly, or they'd
		// swallow unrelated names.
		if len(indexName) < 5 || len(key) < 5 {
			continue
		}
		if strings.Contains(key, indexName) || strings.Contains(indexName, key) {
			return appID
		}
	}
	return ""
}

// manifestNameIndex is the normalized-name -> Steam App ID lookup built from
// the bundled Ludusavi manifest. Built once: the manifest holds tens of
// thousands of entries and a scan asks about every discovery it found.
//
// Ambiguity is dropped rather than guessed. Several distinct titles normalize
// to the same key (re-releases, regional variants), and picking one would
// hand App-ID matching a confident wrong answer — which is worse here than no
// answer, because no answer just means the user links the pair by hand.
// manifestCompactIndex is the same lookup with spaces removed, for folder
// names that never had them: "MinaTheHollower", "ELDENRING". Kept separate
// from manifestNameIndex rather than merged into it, because compaction
// collides more readily — "Portal 2" and "Portal2" are the same game, but so
// are a lot of pairs that are not — and the ambiguity rule below has to be
// applied over the compacted keys to be worth anything.
var manifestCompactIndex = sync.OnceValue(func() map[string]string {
	index := map[string]string{}
	ambiguous := map[string]bool{}
	for key, appID := range manifestNameIndex() {
		compact := strings.ReplaceAll(key, " ", "")
		if compact == "" || ambiguous[compact] {
			continue
		}
		if existing, seen := index[compact]; seen && existing != appID {
			delete(index, compact)
			ambiguous[compact] = true
			continue
		}
		index[compact] = appID
	}
	return index
})

var manifestNameIndex = sync.OnceValue(func() map[string]string {
	games := loadEmbeddedIndex()
	index := make(map[string]string, len(games))
	ambiguous := map[string]bool{}
	for _, g := range games {
		if g.SteamID == "" || g.Name == "" {
			continue
		}
		key := normalizeGameName(g.Name)
		if key == "" || ambiguous[key] {
			continue
		}
		if existing, seen := index[key]; seen && existing != g.SteamID {
			delete(index, key)
			ambiguous[key] = true
			continue
		}
		index[key] = g.SteamID
	}
	return index
})
