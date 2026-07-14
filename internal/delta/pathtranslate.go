package delta

import (
	"os"
	"regexp"
	"strings"
)

// TranslationRule is a user-configured custom path substitution, e.g.
// mapping a Windows-side save location to its Linux/SteamOS equivalent.
type TranslationRule struct {
	FromPattern string `json:"fromPattern"`
	ToPattern   string `json:"toPattern"`
}

var (
	windowsUserProfileRe = regexp.MustCompile(`(?i)^[A-Za-z]:\\Users\\[^\\]+`)
	linuxHomeRe          = regexp.MustCompile(`^/home/[^/]+`)
)

// TranslatePathToLocal converts a peer-reported remote save path into the
// equivalent local path on this machine, applying (in order): the user's
// custom translation rules, then the built-in Windows-%USERPROFILE%
// <-> Linux-/home/<user> substitution, matching delta.js's
// translatePathToLocal().
func TranslatePathToLocal(remotePath string, rules []TranslationRule) string {
	for _, rule := range rules {
		if rule.FromPattern == "" {
			continue
		}
		if strings.HasPrefix(remotePath, rule.FromPattern) {
			rest := strings.TrimPrefix(remotePath, rule.FromPattern)
			rest = normalizeSeparatorsLike(rest, rule.ToPattern)
			return rule.ToPattern + rest
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return remotePath
	}

	if isWindowsPath(remotePath) {
		if loc := windowsUserProfileRe.FindString(remotePath); loc != "" {
			rest := strings.TrimPrefix(remotePath, loc)
			rest = strings.ReplaceAll(rest, `\`, "/")
			return home + rest
		}
	} else if strings.HasPrefix(remotePath, "/home/") {
		if loc := linuxHomeRe.FindString(remotePath); loc != "" {
			rest := strings.TrimPrefix(remotePath, loc)
			return home + filepathFromSlash(rest)
		}
	}

	return remotePath
}

func isWindowsPath(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func filepathFromSlash(p string) string {
	if os.PathSeparator == '/' {
		return p
	}
	return strings.ReplaceAll(p, "/", string(os.PathSeparator))
}

// normalizeSeparatorsLike rewrites the path separators in rest to match
// whichever separator convention target uses, so joining a Windows-style
// remainder onto a Linux-style ToPattern (or vice versa) doesn't leave
// mixed-style separators in the result.
func normalizeSeparatorsLike(rest, target string) string {
	switch {
	case strings.Contains(target, "/"):
		return strings.ReplaceAll(rest, `\`, "/")
	case strings.Contains(target, `\`):
		return strings.ReplaceAll(rest, "/", `\`)
	default:
		return rest
	}
}
