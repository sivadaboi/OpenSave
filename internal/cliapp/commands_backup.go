package cliapp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		body := map[string]any{"targetPath": target}
		if len(args) > 2 {
			games := make([]map[string]string, 0, len(args)-2)
			for _, id := range args[2:] {
				games = append(games, map[string]string{"id": id})
			}
			body["games"] = games
		}

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
		var res struct {
			Count int `json:"count"`
		}
		_ = json.Unmarshal(raw, &res)
		if res.Count > 0 {
			success("Exported %s.", bold(fmt.Sprintf("%d snapshot(s)", res.Count)))
		} else {
			success("Backup written.")
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
		mode := "snapshot"
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
		if mode == "overwrite" {
			success("Restored from backup, overwriting current saves.")
		} else {
			success("Imported as snapshots — nothing on disk was replaced.")
			hint("opensave snapshots <gameId>", "opensave rollback <gameId> <snapshot>")
		}
		note(source)
		return 0

	default:
		fmt.Fprintln(os.Stderr, backupUsage)
		return 1
	}
}

const backupUsage = `usage:
  opensave backup export <file.sscb> [gameId...]   Write a backup archive
                                                   (every game when no ids given)
  opensave backup import <file.sscb> [--overwrite] Read one back

  Import adds the contents as snapshots by default, so nothing on disk is
  replaced. --overwrite restores saves over the current files instead.`
