// Package ignore matches save files against per-game exclusion rules, in the
// style of a .gitignore.
//
// The rules decide what SYNCS, never what is snapshotted. An excluded file is
// still captured in every snapshot and restored by every rollback — and that
// is deliberate, not an oversight. Restoring empties the save folder before
// putting the snapshot back, so a file left out of snapshots would be deleted
// by the first restore. A feature meant to protect a config file cannot be
// allowed to delete it.
package ignore

import (
	"path"
	"strings"
)

// Rules is a compiled set of exclusion patterns for one game.
type Rules struct {
	patterns []pattern
}

type pattern struct {
	// glob is the pattern with any leading "!" or "/" and trailing "/"
	// stripped, lowercased.
	glob string
	// negate re-includes a path an earlier pattern excluded ("!keep.sav").
	negate bool
	// anchored patterns ("/Config.gs") match only at the save root; an
	// unanchored one matches at any depth, as in a .gitignore.
	anchored bool
	// dirOnly patterns ("logs/") match a directory and everything under it.
	dirOnly bool
}

// Parse compiles the text of an ignore list: one pattern per line, "#" for
// comments, blank lines skipped.
//
// Matching is case-insensitive on every platform. Saves move between Windows
// and Linux, and a rule someone wrote on one has to keep working on the other
// — a pattern that silently stopped applying after syncing to a Steam Deck
// would be worse than no feature at all.
func Parse(text string) Rules {
	var r Rules
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := pattern{}
		if strings.HasPrefix(line, "!") {
			p.negate = true
			line = strings.TrimSpace(line[1:])
			if line == "" {
				continue
			}
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			p.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		// A pattern containing a slash is anchored, as in a .gitignore:
		// "saves/Config.gs" means that path from the root, not any file
		// called Config.gs inside any folder called saves.
		if strings.Contains(line, "/") {
			p.anchored = true
		}
		line = strings.ReplaceAll(line, "\\", "/")
		if line == "" {
			continue
		}
		p.glob = strings.ToLower(line)
		r.patterns = append(r.patterns, p)
	}
	return r
}

// Empty reports whether there is nothing to exclude, so callers can skip the
// work entirely for the overwhelming majority of games.
func (r Rules) Empty() bool { return len(r.patterns) == 0 }

// Match reports whether relPath — a slash-separated path relative to the save
// folder — is excluded from syncing.
//
// Later patterns win, so "!" can re-include something an earlier line covered.
func (r Rules) Match(relPath string) bool {
	if len(r.patterns) == 0 {
		return false
	}
	rel := strings.ToLower(strings.Trim(strings.ReplaceAll(relPath, "\\", "/"), "/"))
	if rel == "" {
		return false
	}

	excluded := false
	for _, p := range r.patterns {
		if p.matches(rel) {
			excluded = !p.negate
		}
	}
	return excluded
}

func (p pattern) matches(rel string) bool {
	if p.anchored {
		if globMatch(p.glob, rel) {
			return true
		}
		// A directory pattern also covers everything inside it.
		return p.coversParent(rel)
	}
	// Unanchored: match the path or any of its trailing segments, so
	// "Config.gs" catches "Config.gs" and "sub/Config.gs" alike.
	segments := strings.Split(rel, "/")
	for i := range segments {
		if globMatch(p.glob, strings.Join(segments[i:], "/")) {
			return true
		}
	}
	return p.coversParent(rel)
}

// coversParent reports whether the pattern names a directory that rel sits
// inside. "logs/" has to exclude "logs/today.txt", not merely "logs".
func (p pattern) coversParent(rel string) bool {
	segments := strings.Split(rel, "/")
	for end := 1; end < len(segments); end++ {
		prefix := strings.Join(segments[:end], "/")
		if p.anchored {
			if globMatch(p.glob, prefix) {
				return true
			}
			continue
		}
		for i := 0; i < end; i++ {
			if globMatch(p.glob, strings.Join(segments[i:end], "/")) {
				return true
			}
		}
	}
	return false
}

// globMatch matches a pattern against a path, where "*" stops at a slash and
// "**" crosses them, as in a .gitignore. path.Match gives the first; the
// second is handled by splitting on "**" and matching the parts in order.
func globMatch(glob, name string) bool {
	if !strings.Contains(glob, "**") {
		ok, err := path.Match(glob, name)
		return err == nil && ok
	}

	parts := strings.Split(glob, "**")
	rest := name
	for i, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		idx := indexSegmentMatch(rest, part, i == 0)
		if idx < 0 {
			return false
		}
		rest = idx2rest(rest, idx, part)
	}
	return true
}

// indexSegmentMatch finds where part matches inside name on segment
// boundaries. mustStart requires the match at the very beginning.
func indexSegmentMatch(name, part string, mustStart bool) int {
	segments := strings.Split(name, "/")
	partLen := len(strings.Split(part, "/"))
	for start := 0; start+partLen <= len(segments); start++ {
		if mustStart && start != 0 {
			return -1
		}
		candidate := strings.Join(segments[start:start+partLen], "/")
		if ok, err := path.Match(part, candidate); err == nil && ok {
			return start
		}
	}
	return -1
}

func idx2rest(name string, idx int, part string) string {
	segments := strings.Split(name, "/")
	partLen := len(strings.Split(part, "/"))
	if idx+partLen >= len(segments) {
		return ""
	}
	return strings.Join(segments[idx+partLen:], "/")
}
