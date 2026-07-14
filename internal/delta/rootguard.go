package delta

import (
	"regexp"
	"strings"
)

// DangerousSyncRoot returns a non-empty human-readable reason when path
// points at a profile/system-level folder that must never be treated as a
// game save root. Syncing such a root would hash and transfer the user's
// whole profile (and trip over legacy junctions on the way). This happened
// in the wild: a peer ended up with a game tracked at the profile root,
// and every sync died on C:\Users\<name>\AppData\Local\Application Data.
//
// Returns "" for paths that are fine to sync.
func DangerousSyncRoot(path string) string {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return "the save path is empty"
	}
	// Normalize to backslash-separated with no trailing separator so the
	// checks below are separator- and case-insensitive.
	p = strings.ReplaceAll(p, "/", `\`)
	p = strings.TrimRight(p, `\`)

	if isWindowsStyle(p) {
		return dangerousWindowsRoot(p)
	}
	return dangerousUnixRoot(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(path)), `\`, "/"))
}

var driveRe = regexp.MustCompile(`^[a-z]:$`)

func isWindowsStyle(p string) bool {
	return len(p) >= 2 && p[1] == ':'
}

func dangerousWindowsRoot(p string) string {
	if driveRe.MatchString(p) {
		return "it is a whole drive"
	}
	segs := strings.Split(p, `\`)
	// segs[0] is the drive ("c:").
	rest := segs[1:]

	if len(rest) >= 1 {
		switch rest[0] {
		case "windows":
			return "it is inside the Windows system folder"
		case "users":
			// c:\users, c:\users\<name>, and the well-known top-level
			// profile folders are all far too broad to sync.
			switch len(rest) {
			case 1:
				return "it is the Users folder"
			case 2:
				return "it is a whole user profile folder"
			case 3, 4:
				switch rest[2] {
				case "appdata":
					// AppData itself and its Local/Roaming/LocalLow tiers.
					if len(rest) == 3 {
						return "it is the whole AppData folder"
					}
					switch rest[3] {
					case "local", "roaming", "locallow":
						return "it is a whole application-data folder"
					}
				case "documents", "desktop", "downloads", "pictures", "music", "videos", "saved games", "onedrive":
					if len(rest) == 3 {
						return "it is a whole " + rest[2] + " folder"
					}
				}
			}
		case "program files", "program files (x86)", "programdata":
			if len(rest) == 1 {
				return "it is the whole " + rest[0] + " folder"
			}
		}
	}
	return ""
}

func dangerousUnixRoot(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "it is the filesystem root"
	}
	switch p {
	case "/home", "/root", "/etc", "/usr", "/var", "/tmp":
		return "it is a system folder"
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if segs[0] == "home" {
		switch len(segs) {
		case 2:
			return "it is a whole home folder"
		case 3, 4:
			switch segs[2] {
			case ".config", ".local", ".steam":
				if len(segs) == 3 {
					return "it is a whole " + segs[2] + " folder"
				}
				if segs[2] == ".local" && (segs[3] == "share") {
					return "it is a whole .local/share folder"
				}
			case "documents", "desktop", "downloads":
				if len(segs) == 3 {
					return "it is a whole " + segs[2] + " folder"
				}
			}
		}
	}
	return ""
}
