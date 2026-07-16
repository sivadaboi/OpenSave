package presets

import "strings"

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

// inferAppIDFromName matches a discovered folder/game name against the
// popular-games index: exact match first, then bidirectional substring
// containment (folder "EldenRing Backup" matches "elden ring", and vice
// versa), matching the JS heuristic.
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
