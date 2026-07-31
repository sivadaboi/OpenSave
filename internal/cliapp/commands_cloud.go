package cliapp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cloud backup from the terminal, including sign-in.
//
// This file used to say OAuth providers could not be connected headlessly and
// had to be set up in the desktop app. That was never true of the flow, only
// of the CLI: the app calls /api/auth/start, sends a human to a browser, and
// posts the returned code to /api/auth/callback. Nothing in that requires the
// browser to be on the same machine — the redirect goes to a localhost URL
// that simply fails to load elsewhere, leaving the code visible in the address
// bar to copy back.
//
// It also claimed WebDAV and friends were configurable "entirely from here"
// with `opensave cloud setup`, a command that did not exist. Both are now
// real, so a headless server or a Deck in Game Mode can set up cloud backup
// without a desktop anywhere in the process.

func cmdCloud(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, cloudUsage)
		return 1
	}

	switch args[0] {
	case "connect":
		return cloudConnect(asJSON, args[1:])
	case "setup":
		return cloudSetup(asJSON, args[1:])
	case "disconnect":
		return cloudDisconnect(asJSON)
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
  opensave cloud connect <provider>       Sign in to Google Drive, Dropbox or OneDrive
  opensave cloud setup <provider> [opts]  Configure WebDAV, a webhook, or a local folder
  opensave cloud disconnect               Forget the current provider
  opensave cloud status                   Provider, and whether it's connected
  opensave cloud browse                   Everything stored in the cloud
  opensave cloud list <gameId>            Cloud snapshots for one game
  opensave cloud push <gameId>            Upload this game's local snapshots
  opensave cloud restore <gameId> <file>  Pull a cloud snapshot back
  opensave cloud delete <gameId>          Remove a game's cloud copies

  providers: google-drive, dropbox, onedrive, webdav, webhook, local

  Signing in needs a browser, but not this machine's browser — connect prints
  a link you can open anywhere and paste the code back.`

// providerID maps the names people type to the ids the daemon uses. The
// stored value is google_drive; nobody types an underscore.
func providerID(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "google-drive", "google_drive", "google", "drive", "gdrive":
		return "google_drive"
	case "dropbox":
		return "dropbox"
	case "onedrive", "one-drive":
		return "onedrive"
	case "webdav":
		return "webdav"
	case "webhook":
		return "webhook"
	case "local", "folder", "nas":
		return "local"
	default:
		return ""
	}
}

// cloudConnect runs the OAuth sign-in from a terminal.
//
// This was described as desktop-only, and it never needed to be. The flow the
// app uses is two daemon calls with a browser in between, and the browser does
// not have to be on this machine: the redirect lands on http://localhost/callback,
// which will fail to load on a phone or another PC, but the code is sitting in
// the address bar of that failed page. Paste it back and the exchange happens
// here. That is the difference between a headless server or a Deck in Game Mode
// being able to use cloud backup and not.
func cloudConnect(asJSON bool, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr,
			"usage: opensave cloud connect <google-drive|dropbox|onedrive>\n"+
				"  For WebDAV, a webhook or a local folder use `opensave cloud setup`.")
		return 1
	}
	provider := providerID(args[0])
	switch provider {
	case "google_drive", "dropbox", "onedrive":
	case "":
		return fail(asJSON, fmt.Errorf("unknown provider %q", args[0]))
	default:
		return fail(asJSON, fmt.Errorf("%s does not sign in — configure it with `opensave cloud setup %s`", args[0], args[0]))
	}

	raw, err := daemonRequest("POST", "/api/auth/start", map[string]any{"provider": provider})
	if err != nil {
		return fail(asJSON, err)
	}
	var start struct {
		AuthURL      string `json:"authUrl"`
		AutoCallback bool   `json:"autoCallback"`
	}
	if err := json.Unmarshal(raw, &start); err != nil || start.AuthURL == "" {
		return fail(asJSON, fmt.Errorf("the daemon did not return a sign-in link"))
	}

	if asJSON {
		// Scripted use gets the link and stops: the next step needs a human
		// at a browser, and pretending otherwise would just hang.
		return emitJSON(map[string]any{
			"provider": provider, "authUrl": start.AuthURL,
			"autoCallback": start.AutoCallback,
			"next":         "open authUrl, approve, then: opensave cloud connect " + args[0] + " --code <code>",
		})
	}

	// A code passed up front is the second half of the flow, for anyone who
	// already has one from a previous invocation.
	if code := flagValue(args[1:], "--code"); code != "" {
		return cloudFinishAuth(asJSON, code)
	}

	section("Connect " + provider)
	fmt.Println()
	fmt.Println("  1. Open this link and approve access:")
	fmt.Println()
	fmt.Printf("     %s\n", accent(start.AuthURL))
	fmt.Println()
	if start.AutoCallback {
		note("This machine is listening for the redirect, so if you open the link here it may finish on its own.")
		hint("opensave cloud status     check whether it completed")
		fmt.Println()
	}
	fmt.Println("  2. The browser lands on a localhost page that won't load. That's expected —")
	fmt.Println("     the part you need is in its address bar: code=<something>")
	fmt.Println()
	fmt.Println("  3. Paste it back:")
	fmt.Println()
	fmt.Printf("     %s\n", faint("opensave cloud connect "+args[0]+" --code <code>"))
	fmt.Println()
	return 0
}

func cloudFinishAuth(asJSON bool, code string) int {
	raw, err := daemonRequest("POST", "/api/auth/callback", map[string]any{"code": code})
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	var res struct {
		UserEmail string `json:"userEmail"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.UserEmail != "" {
		success("Connected as %s", bold(res.UserEmail))
	} else {
		success("Connected.")
	}
	hint("opensave cloud push <gameId>")
	return 0
}

// cloudSetup configures the providers that need settings rather than a
// sign-in. The comment at the top of this file has claimed since it was
// written that these "work entirely from here" via `opensave cloud setup` —
// the command did not exist.
func cloudSetup(asJSON bool, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, cloudSetupUsage)
		return 1
	}
	provider := providerID(args[0])
	rest := args[1:]

	patch := map[string]any{"enabled": true, "provider": provider}
	switch provider {
	case "local":
		path := flagValue(rest, "--path")
		if path == "" {
			return fail(asJSON, fmt.Errorf("a local destination needs --path <folder>"))
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return fail(asJSON, err)
		}
		patch["url"] = abs

	case "webdav":
		u := flagValue(rest, "--url")
		if u == "" {
			return fail(asJSON, fmt.Errorf("webdav needs --url <https://...>"))
		}
		patch["url"] = u
		if v := flagValue(rest, "--user"); v != "" {
			patch["username"] = v
		}
		if v := flagValue(rest, "--password"); v != "" {
			patch["password"] = v
		}

	case "webhook":
		u := flagValue(rest, "--url")
		if u == "" {
			return fail(asJSON, fmt.Errorf("a webhook needs --url <https://...>"))
		}
		patch["url"] = u
		if v := flagValue(rest, "--headers"); v != "" {
			patch["headers"] = v
		}

	case "google_drive", "dropbox", "onedrive":
		return fail(asJSON, fmt.Errorf("%s signs in rather than being configured — use `opensave cloud connect %s`", args[0], args[0]))
	default:
		return fail(asJSON, fmt.Errorf("unknown provider %q\n\n%s", args[0], cloudSetupUsage))
	}

	if _, err := daemonRequest("POST", "/api/settings", map[string]any{"cloudSync": patch}); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{"provider": provider, "configured": true})
	}
	success("Cloud backup set to %s", bold(provider))
	hint("opensave cloud status", "opensave cloud push <gameId>")
	return 0
}

const cloudSetupUsage = `usage:
  opensave cloud setup local   --path <folder>
  opensave cloud setup webdav  --url <url> [--user <u>] [--password <p>]
  opensave cloud setup webhook --url <url> [--headers '<json>']

  Google Drive, Dropbox and OneDrive sign in instead:
  opensave cloud connect <provider>`

func cloudDisconnect(asJSON bool) int {
	if _, err := daemonRequest("POST", "/api/auth/disconnect", map[string]any{}); err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitJSON(map[string]any{"disconnected": true})
	}
	success("Disconnected. Snapshots already in the cloud are left alone.")
	return 0
}

// flagValue reads "--name value" out of an argument list.
func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

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

	// The endpoint groups by game — {gameId, gameName, count, totalSize,
	// snapshots:[…]} — and this decoded a flat list of files. Only gameId sat
	// where it was looking, so every row printed a game with no filename, no
	// branch and 0 B. Nothing errored: the fields simply were not there.
	games, err := decodeCloudBrowse(raw)
	if err != nil {
		return emitRawJSON(raw)
	}
	total := 0
	for _, g := range games {
		total += len(g.Snapshots)
	}
	if total == 0 {
		section("Cloud backup")
		note("Nothing stored in the cloud yet.")
		hint("opensave cloud push <gameId>")
		fmt.Println()
		return 0
	}

	section(fmt.Sprintf("Cloud backup %s %d file(s) across %d game(s)", symDot(), total, len(games)))
	t := newTable("game", "branch", "size", "file")
	for _, g := range games {
		label := g.GameName
		if label == "" {
			label = g.GameID
		}
		for _, s := range g.Snapshots {
			t.add(bold(label), faint(s.Branch), humanBytes(s.SizeBytes), faint(s.Name))
		}
	}
	t.render()
	hint("opensave cloud restore <gameId> <file>")
	fmt.Println()
	return 0
}

// cloudBrowseGame is one game's group in the browse listing. Extracted with
// its decoder so a literal of the real payload can hold the shape still —
// this is the third place the CLI has read field names the daemon does not
// send, and every one of them failed silently rather than erroring.
type cloudBrowseGame struct {
	GameID    string `json:"gameId"`
	GameName  string `json:"gameName"`
	Count     int    `json:"count"`
	TotalSize int64  `json:"totalSize"`
	Snapshots []struct {
		Name        string `json:"name"`
		SizeBytes   int64  `json:"sizeBytes"`
		Branch      string `json:"branch"`
		SnapshotID  string `json:"snapshotId"`
		CreatedTime string `json:"createdTime"`
	} `json:"snapshots"`
}

func decodeCloudBrowse(raw []byte) ([]cloudBrowseGame, error) {
	var games []cloudBrowseGame
	if err := json.Unmarshal(raw, &games); err != nil {
		return nil, err
	}
	return games, nil
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

	// The field is "name", not "fileName". Reading the wrong one left the
	// FILE column blank — and that column is the argument `cloud restore`
	// takes, so the restore flow could not be completed from here at all:
	// the hint told you to pass a filename the listing declined to show.
	var snaps []struct {
		FileName   string `json:"name"`
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
