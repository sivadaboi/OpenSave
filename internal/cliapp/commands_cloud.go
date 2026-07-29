package cliapp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Cloud backup from the terminal.
//
// One deliberate omission: connecting a provider that uses OAuth (Google
// Drive, Dropbox, OneDrive) needs a browser to complete the consent flow, so
// it can't be done headlessly and isn't pretended otherwise. Set those up once
// in the desktop app; WebDAV, a webhook, or a local/NAS folder can be
// configured entirely from here with `opensave cloud setup`.

func cmdCloud(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, cloudUsage)
		return 1
	}

	switch args[0] {
	case "status":
		return cloudStatus(asJSON)
	case "list":
		return cloudList(asJSON, args[1:])
	case "browse":
		return cloudBrowse(asJSON)
	case "push":
		return cloudPush(asJSON, args[1:])
	case "restore":
		return cloudRestore(asJSON, args[1:])
	case "delete":
		return cloudDelete(asJSON, args[1:])
	default:
		fmt.Fprintln(os.Stderr, cloudUsage)
		return 1
	}
}

const cloudUsage = `usage:
  opensave cloud status                   Provider, and whether it's connected
  opensave cloud browse                   Everything stored in the cloud
  opensave cloud list <gameId>            Cloud snapshots for one game
  opensave cloud push <gameId>            Upload this game's local snapshots
  opensave cloud restore <gameId> <file>  Pull a cloud snapshot back
  opensave cloud delete <gameId>          Remove a game's cloud copies

  Google Drive, Dropbox and OneDrive need a browser to authorise, so connect
  those once in the desktop app. WebDAV, webhook and local/NAS folders work
  entirely from here.`

func cloudSettings() (map[string]any, error) {
	raw, err := daemonRequest("GET", "/api/settings", nil)
	if err != nil {
		return nil, err
	}
	var s struct {
		CloudSync map[string]any `json:"cloudSync"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.CloudSync == nil {
		return map[string]any{}, nil
	}
	return s.CloudSync, nil
}

func cloudStatus(asJSON bool) int {
	cfg, err := cloudSettings()
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(cfg)
	}

	provider, _ := cfg["provider"].(string)
	enabled, _ := cfg["enabled"].(bool)

	section("Cloud backup")
	if provider == "" || provider == "local" && !enabled {
		field("provider", faint("none configured"))
		hint("opensave cloud status --json     see the raw settings",
			"set a provider up in the desktop app, or use a local/NAS folder")
		fmt.Println()
		return 0
	}
	field("provider", accent(provider))
	if enabled {
		field("state", okText("enabled"))
	} else {
		field("state", faint("disabled"))
	}
	if u, _ := cfg["url"].(string); u != "" {
		field("url", faint(u))
	}
	if email, _ := cfg["userEmail"].(string); email != "" {
		field("account", faint(email))
	}
	if folder, _ := cfg["folderId"].(string); folder != "" {
		field("folder", faint(folder))
	}
	fmt.Println()
	return 0
}

func cloudBrowse(asJSON bool) int {
	raw, err := daemonRequest("GET", "/api/cloud/browse", nil)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}

	var entries []struct {
		Name      string `json:"name"`
		GameID    string `json:"gameId"`
		Branch    string `json:"branch"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return emitRawJSON(raw)
	}
	if len(entries) == 0 {
		section("Cloud backup")
		note("Nothing stored in the cloud yet.")
		hint("opensave cloud push <gameId>")
		fmt.Println()
		return 0
	}

	section(fmt.Sprintf("Cloud backup %s %d file(s)", symDot(), len(entries)))
	t := newTable("game", "branch", "size", "file")
	for _, e := range entries {
		t.add(bold(e.GameID), faint(e.Branch), humanBytes(e.SizeBytes), faint(e.Name))
	}
	t.render()
	fmt.Println()
	return 0
}

func cloudList(asJSON bool, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave cloud list <gameId>")
		return 1
	}
	raw, err := daemonRequest("GET", "/api/cloud/snapshots/"+args[0], nil)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}

	var snaps []struct {
		FileName   string `json:"fileName"`
		SnapshotID string `json:"snapshotId"`
		Branch     string `json:"branch"`
		SizeBytes  int64  `json:"sizeBytes"`
	}
	if json.Unmarshal(raw, &snaps) != nil {
		return emitRawJSON(raw)
	}
	if len(snaps) == 0 {
		section(args[0])
		note("No cloud snapshots for this game.")
		hint("opensave cloud push " + args[0])
		fmt.Println()
		return 0
	}

	section(fmt.Sprintf("%s %s cloud snapshots", args[0], symDot()))
	t := newTable("snapshot", "branch", "size", "file")
	for _, s := range snaps {
		t.add(accent(s.SnapshotID), faint(s.Branch), humanBytes(s.SizeBytes), faint(s.FileName))
	}
	t.render()
	hint("opensave cloud restore " + args[0] + " <file>")
	fmt.Println()
	return 0
}

func cloudPush(asJSON bool, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave cloud push <gameId>")
		return 1
	}
	raw, err := daemonRequest("POST", "/api/cloud/sync-local/"+args[0], map[string]any{})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	var res struct {
		Uploaded int `json:"uploaded"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.Uploaded > 0 {
		success("Uploaded %d snapshot(s) for %s", res.Uploaded, bold(args[0]))
	} else {
		success("%s is already up to date in the cloud.", bold(args[0]))
	}
	return 0
}

func cloudRestore(asJSON bool, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr,
			"usage: opensave cloud restore <gameId> <fileName>\n"+
				"  list the file names with `opensave cloud list <gameId>`")
		return 1
	}
	raw, err := daemonRequest("POST", "/api/cloud/restore/"+args[0],
		map[string]any{"fileName": args[1]})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	success("Restored %s from the cloud", accent(args[1]))
	note("It landed as a snapshot — roll back to it to replace the live save.")
	hint("opensave snapshots " + args[0])
	return 0
}

func cloudDelete(asJSON bool, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave cloud delete <gameId> [--yes]")
		return 1
	}
	confirmed := false
	for _, a := range args[1:] {
		if a == "--yes" || a == "-y" {
			confirmed = true
		}
	}
	if !confirmed {
		if asJSON {
			return fail(asJSON, fmt.Errorf("refusing to delete cloud copies without --yes"))
		}
		warning("This deletes every cloud copy of %s.", bold(args[0]))
		note("Local snapshots on this device are not touched.")
		hint("opensave cloud delete " + args[0] + " --yes")
		return 1
	}

	raw, err := daemonRequest("POST", "/api/cloud/delete-game/"+args[0], map[string]any{})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	var res struct {
		Deleted int `json:"deleted"`
	}
	_ = json.Unmarshal(raw, &res)
	success("Removed %d cloud file(s) for %s", res.Deleted, bold(args[0]))
	return 0
}

// ── Snapshot contents ────────────────────────────────────────────────────

// cmdFiles lists what's inside a snapshot, and can restore a single file —
// the app's snapshot browser, which had no CLI equivalent.
// snapshotFile is one entry from a snapshot's contents listing.
//
// The field names are the whole point of this type existing. The snapshot
// listing sends "size", while the cloud endpoints in this same file send
// "sizeBytes" — and reading the wrong one unmarshals to zero without an
// error, so the listing printed every save as 0 B and looked like the
// snapshots were empty rather than like a decoding mistake. Extracted so a
// literal of the real payload can hold the names still.
type snapshotFile struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
}

func decodeSnapshotFiles(raw []byte) ([]snapshotFile, error) {
	var files []snapshotFile
	if err := json.Unmarshal(raw, &files); err != nil {
		return nil, err
	}
	return files, nil
}

func cmdFiles(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, filesUsage)
		return 1
	}
	gameID, snapID := args[0], args[1]

	// Third argument means "restore just this file".
	if len(args) >= 3 {
		// The field is relPath; sending "path" made every single-file restore
		// fail with "relPath is required" and restore nothing.
		raw, err := daemonRequest("POST",
			fmt.Sprintf("/api/games/%s/snapshot/%s/restore-file", gameID, snapID),
			map[string]any{"relPath": args[2]})
		if err != nil {
			return fail(asJSON, err)
		}
		if asJSON {
			return emitRawJSON(raw)
		}
		success("Restored %s from %s", bold(args[2]), accent(snapID))
		return 0
	}

	raw, err := daemonRequest("GET",
		fmt.Sprintf("/api/games/%s/snapshot/%s/files", gameID, snapID), nil)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}

	files, err := decodeSnapshotFiles(raw)
	if err != nil {
		return emitRawJSON(raw)
	}
	if len(files) == 0 {
		section(snapID)
		note("This snapshot contains no files.")
		fmt.Println()
		return 0
	}

	section(fmt.Sprintf("%s %s %d file(s)", snapID, symDot(), len(files)))
	t := newTable("size", "path")
	for _, f := range files {
		// A directory has no size worth printing, and "0 B" next to one reads
		// as an empty file rather than a folder.
		size := humanBytes(f.Size)
		if f.IsDir {
			size = sym("—", "-")
		}
		t.add(faint(size), f.Path)
	}
	t.render()
	hint(fmt.Sprintf("opensave files %s %s <path>     restore just one file", gameID, snapID))
	fmt.Println()
	return 0
}

const filesUsage = `usage:
  opensave files <gameId> <snapshotId>          List what's in a snapshot
  opensave files <gameId> <snapshotId> <path>   Restore a single file from it`

// ── Peers ────────────────────────────────────────────────────────────────

// cmdProbe checks whether a device answers at an address, before you try to
// pair with it. The API probes by address rather than peer id, so this works
// for devices you haven't paired with yet — which is when you most want it.
func cmdProbe(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: opensave probe <host[:port]>")
		return 1
	}

	host := args[0]
	port := 8383
	if h, p, ok := strings.Cut(host, ":"); ok {
		host = h
		fmt.Sscanf(p, "%d", &port)
	}

	raw, err := daemonRequest("POST", "/api/peers/probe",
		map[string]any{"address": host, "port": port})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	var res struct {
		Reachable bool   `json:"reachable"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(raw, &res)
	target := fmt.Sprintf("%s:%d", host, port)
	if res.Reachable {
		success("%s is reachable.", bold(target))
		hint("opensave pair " + target)
		return 0
	}
	warning("%s did not answer.", bold(target))
	if res.Error != "" {
		note(res.Error)
	}
	note("Check OpenSave is running there, and that a firewall isn't blocking the port.")
	return 1
}

// cmdForget removes a peer record outright. Unpair is the polite version that
// tells the other device; this is for entries that are already stale.
func cmdForget(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr,
			"usage: opensave forget <peerId>\n"+
				"  Removes a stale device record. Use `unpair` for a device that\n"+
				"  still exists, so it's told about it too.")
		return 1
	}
	if _, err := daemonRequest("DELETE", "/api/peers/"+strings.TrimSpace(args[0]), nil); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{"forgotten": args[0]})
	}
	success("Removed %s.", bold(args[0]))
	return 0
}
