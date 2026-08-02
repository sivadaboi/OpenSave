package cliapp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Backup archives (.sscb): the "move everything to a new machine" and "keep a
// copy off this disk" path. Previously only the desktop app could produce or
// consume one, which left a headless install with no migration story.

func cmdBackup(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, backupUsage)
		return 1
	}

	switch args[0] {
	case "export":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave backup export <file.sscb> [gameId...]")
			return 1
		}
		target, err := filepath.Abs(args[1])
		if err != nil {
			return fail(asJSON, err)
		}

		// No ids means everything, matching the app's "export all" button.
		//
		// "Everything" still has to be spelled out as a game list. The
		// endpoint falls back to a snapshot-library export when none is
		// given, and that older format records only snapshot archives —
		// no names, no save paths. Restoring one onto a fresh install
		// therefore matches nothing and silently restores nothing, which is
		// the one moment a backup has to work. Listing the games produces the
		// same archive the desktop app writes, which carries the manifest a
		// clean machine needs.
		ids := args[2:]
		if len(ids) == 0 {
			var err error
			ids, err = trackedGameIDs()
			if err != nil {
				return fail(asJSON, err)
			}
			if len(ids) == 0 {
				return fail(asJSON, fmt.Errorf("nothing is tracked, so there is nothing to back up"))
			}
		}
		games := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			games = append(games, map[string]string{"id": id})
		}
		body := map[string]any{"targetPath": target, "games": games}

		raw, err := daemonRequest("POST", "/api/backup/export", body)
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}

		out := target
		if !strings.HasSuffix(strings.ToLower(out), ".sscb") {
			out += ".sscb"
		}
		// The endpoint answers in one of two shapes: exporting a selection of
		// games reports "exported", exporting everything reports
		// "snapshotCount". Neither is called "count", which is what this read
		// until now — so the number was always zero and every successful
		// export printed the bare "Backup written.", giving no way to tell a
		// full backup from one that captured nothing.
		var res struct {
			SnapshotCount int `json:"snapshotCount"`
			Exported      int `json:"exported"`
		}
		_ = json.Unmarshal(raw, &res)
		switch {
		case res.Exported > 0:
			success("Exported %s.", bold(fmt.Sprintf("%d game(s)", res.Exported)))
		case res.SnapshotCount > 0:
			success("Exported %s.", bold(fmt.Sprintf("%d snapshot(s)", res.SnapshotCount)))
		default:
			success("Backup written, but it captured nothing.")
		}
		note(out)
		if info, statErr := os.Stat(out); statErr == nil {
			note(humanBytes(info.Size()))
		}
		return 0

	case "import", "restore":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: opensave backup import <file.sscb> [--overwrite]")
			return 1
		}
		source, err := filepath.Abs(args[1])
		if err != nil {
			return fail(asJSON, err)
		}
		if _, err := os.Stat(source); err != nil {
			return fail(asJSON, fmt.Errorf("no backup at %s", source))
		}

		// Default is the non-destructive mode: incoming saves land as
		// snapshots you can roll back to, rather than replacing what's on disk.
		//
		// The endpoint spells it "snapshots". The singular was sent here for
		// as long as this command has existed and was never rejected, because
		// only the legacy restore path could be reached from the CLI and that
		// path ignores the mode entirely. The moment exports started carrying
		// a manifest, every default import failed outright.
		mode := "snapshots"
		for _, a := range args[2:] {
			if a == "--overwrite" {
				mode = "overwrite"
			}
		}

		raw, err := daemonRequest("POST", "/api/backup/restore", map[string]any{
			"sourcePath": source,
			"mode":       mode,
		})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}

		// Say what actually came back. Reporting a flat success was actively
		// misleading for the case that matters most: an older archive holds
		// only snapshot files, and every entry whose game is not already
		// tracked is skipped — so restoring onto a fresh install produced a
		// cheerful message and an empty app, with the reason only in the log.
		// Field names are the endpoint's, not the obvious ones: the v2 reply
		// counts "restored" and "snapshots", the legacy reply counts
		// "imported". Getting one wrong is silent — it unmarshals to zero and
		// a successful import reports having restored nothing.
		var res struct {
			Restored    int  `json:"restored"`
			Snapshotted int  `json:"snapshots"`
			Imported    int  `json:"imported"`
			Skipped     int  `json:"skipped"`
			Legacy      bool `json:"legacy"`
		}
		_ = json.Unmarshal(raw, &res)
		done := res.Restored + res.Snapshotted + res.Imported

		if done == 0 {
			warning("Nothing was restored from this backup.")
			if res.Legacy {
				note("It is an older archive that stores snapshots only — it can restore " +
					"them for games this device already tracks, but it carries no save " +
					"locations, so it cannot recreate them on a fresh install.")
				hint("opensave add <name> <path>", "opensave backup import "+source)
			} else if res.Skipped > 0 {
				note(fmt.Sprintf("%d entr%s skipped.", res.Skipped, entryPlural(res.Skipped)))
				if mode == "snapshots" {
					// The default mode only adds to the history of games this
					// device already tracks, so restoring onto a fresh install
					// skips everything. That is the most likely reason someone
					// is running this command at all, so say what to do next
					// rather than leaving them to read the activity log.
					note("Importing as snapshots only adds to games this device already " +
						"tracks. Nothing here is tracked yet, so use --overwrite to put " +
						"the saves back and track them.")
					hint("opensave backup import " + source + " --overwrite")
				}
			}
			note(source)
			return 1
		}

		switch {
		case mode == "overwrite":
			success("Restored %s, overwriting current saves.", bold(fmt.Sprintf("%d game(s)", done)))
		case res.Legacy:
			success("Imported %s.", bold(fmt.Sprintf("%d snapshot(s)", done)))
		default:
			success("Imported %s as snapshots — nothing on disk was replaced.",
				bold(fmt.Sprintf("%d game(s)", done)))
			hint("opensave snapshots <gameId>", "opensave rollback <gameId> <snapshot>")
		}
		if res.Skipped > 0 {
			note(fmt.Sprintf("%d entr%s skipped — see the activity log for why.",
				res.Skipped, entryPlural(res.Skipped)))
		}
		note(source)
		return 0

	default:
		fmt.Fprintln(os.Stderr, backupUsage)
		return 1
	}
}

// trackedGameIDs asks the running daemon what is tracked, so "back up
// everything" can name the games rather than falling through to the older
// format that cannot describe them.
func trackedGameIDs() ([]string, error) {
	raw, err := daemonRequest("GET", "/api/games", nil)
	if err != nil {
		return nil, err
	}
	// The payload is keyed by game id.
	var byID map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byID); err != nil {
		return nil, fmt.Errorf("reading the game list: %w", err)
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids) // stable order, so two exports of the same set match
	return ids, nil
}

func entryPlural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

const backupUsage = `usage:
  opensave backup export <file.sscb> [gameId...]   Write a backup archive
                                                   (every game when no ids given)
  opensave backup import <file.sscb> [--overwrite] Read one back

  Import adds the contents as snapshots by default, so nothing on disk is
  replaced. --overwrite restores saves over the current files instead.`
