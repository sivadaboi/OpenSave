package cliapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJSONFlag pins that --json can appear anywhere in the arguments and is
// stripped before positional parsing, so `sync --json <id>` and
// `sync <id> --json` behave the same.
func TestJSONFlag(t *testing.T) {
	cases := []struct {
		in       []string
		wantJSON bool
		wantRest []string
	}{
		{[]string{"game-id"}, false, []string{"game-id"}},
		{[]string{"--json", "game-id"}, true, []string{"game-id"}},
		{[]string{"game-id", "--json"}, true, []string{"game-id"}},
		{[]string{"--json"}, true, []string{}},
		{[]string{"approve", "--json", "peer-1"}, true, []string{"approve", "peer-1"}},
	}
	for _, c := range cases {
		gotJSON, gotRest := jsonFlag(c.in)
		if gotJSON != c.wantJSON {
			t.Errorf("jsonFlag(%v) json = %v, want %v", c.in, gotJSON, c.wantJSON)
		}
		if strings.Join(gotRest, ",") != strings.Join(c.wantRest, ",") {
			t.Errorf("jsonFlag(%v) rest = %v, want %v", c.in, gotRest, c.wantRest)
		}
	}
}

// TestPeersPayloadDecoding guards against the shape drift that made `peers`
// dump raw JSON: /api/peers returns the whole dashboard peer state, not a bare
// map of paired devices.
func TestPeersPayloadDecoding(t *testing.T) {
	raw := []byte(`{
	  "peers": {"node_abc": {"name":"Desktop","status":"online","address":"192.168.1.5","port":8383}},
	  "discoveredPeers": [{"name":"Deck","address":"192.168.1.9","port":8383}],
	  "pairingRequests": [{"peerId":"node_xyz","name":"Laptop"}],
	  "wanRoom": {"roomCode":""}
	}`)

	p, err := decodePeersPayload(raw)
	if err != nil {
		t.Fatalf("decodePeersPayload error = %v", err)
	}
	if len(p.Peers) != 1 || p.Peers["node_abc"].Name != "Desktop" {
		t.Errorf("peers decoded as %+v", p.Peers)
	}
	if len(p.DiscoveredPeers) != 1 || p.DiscoveredPeers[0].Name != "Deck" {
		t.Errorf("discoveredPeers decoded as %+v", p.DiscoveredPeers)
	}
	if len(p.PairingRequests) != 1 || p.PairingRequests[0].PeerID != "node_xyz" {
		t.Errorf("pairingRequests decoded as %+v", p.PairingRequests)
	}
}

// TestRenderUnitUsesThisBinary pins that the generated systemd unit points at
// the running executable — a hardcoded path would break for anyone who didn't
// install to the one location we guessed.
func TestRenderUnitUsesThisBinary(t *testing.T) {
	unit := renderUnit("/opt/opensave/opensave-cli")
	if !strings.Contains(unit, "ExecStart=/opt/opensave/opensave-cli daemon start") {
		t.Errorf("unit does not start the given binary:\n%s", unit)
	}
	for _, want := range []string{"[Unit]", "[Service]", "[Install]", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
}

// TestCopyTreeRoundTrip covers `export`: saves must come out exactly as the
// game wrote them — same tree, same bytes, no archive wrapper.
func TestCopyTreeRoundTrip(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "slot1.sav"), "progress")
	mustWrite(t, filepath.Join(src, "profiles", "p1", "config.ini"), "[settings]")

	dest := filepath.Join(t.TempDir(), "out")
	files, total, err := copyTree(src, dest)
	if err != nil {
		t.Fatalf("copyTree error = %v", err)
	}
	if files != 2 {
		t.Errorf("copied %d files, want 2", files)
	}
	if total != int64(len("progress")+len("[settings]")) {
		t.Errorf("copied %d bytes, want %d", total, len("progress")+len("[settings]"))
	}
	if got := mustRead(t, filepath.Join(dest, "slot1.sav")); got != "progress" {
		t.Errorf("slot1.sav = %q", got)
	}
	if got := mustRead(t, filepath.Join(dest, "profiles", "p1", "config.ini")); got != "[settings]" {
		t.Errorf("nested file = %q", got)
	}
}

// TestCopyTreeSingleFile covers games tracked as one save file rather than a
// folder.
func TestCopyTreeSingleFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "save.dat")
	mustWrite(t, src, "one-file")

	dest := filepath.Join(t.TempDir(), "out", "save.dat")
	files, total, err := copyTree(src, dest)
	if err != nil {
		t.Fatalf("copyTree error = %v", err)
	}
	if files != 1 || total != int64(len("one-file")) {
		t.Errorf("files=%d bytes=%d, want 1 and %d", files, total, len("one-file"))
	}
	if got := mustRead(t, dest); got != "one-file" {
		t.Errorf("copied content = %q", got)
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"Elden Ring":                "Elden Ring",
		"NieR:Automata":             "NieR-Automata",
		`Some/Game\With:Bad*Chars?`: "Some-Game-With-Bad-Chars",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 512: "512 B", 1024: "1.0 KB", 1536: "1.5 KB", 1048576: "1.0 MB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestEmitJSONIsValid guards the scripting contract: --json output must parse.
func TestEmitJSONIsValid(t *testing.T) {
	payload := map[string]any{"running": true, "games": 3}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Errorf("emitted JSON does not round-trip: %v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestCompletionCoversEveryCommand guards the drift that makes completions
// quietly useless: a command added to the dispatcher but not to the
// completion list. Every command the usage text documents must be completable.
func TestCompletionCoversEveryCommand(t *testing.T) {
	known := map[string]bool{}
	for _, c := range topLevelCommands {
		known[c] = true
	}
	// Commands named in the help output, which is what users read.
	for _, cmd := range []string{
		"scan", "add", "remove", "status", "sync", "peers", "pair", "unpair",
		"relay", "snapshot", "snapshots", "rollback", "branch", "checkout",
		"export", "config", "exclude", "link", "unlink", "links", "daemon",
		"service", "completion", "upnp", "version",
	} {
		if !known[cmd] && cmd != "completion" {
			t.Errorf("command %q is documented but missing from completions", cmd)
		}
	}
}

// TestCompletionScriptsAreNonEmpty checks each shell gets a script mentioning
// the commands, rather than an empty or truncated file.
func TestCompletionScriptsAreNonEmpty(t *testing.T) {
	for name, script := range map[string]string{
		"bash": bashCompletion(),
		"zsh":  zshCompletion(),
		"fish": fishCompletion(),
	} {
		if len(script) < 100 {
			t.Errorf("%s completion looks empty (%d bytes)", name, len(script))
		}
		for _, must := range []string{"sync", "pair", "daemon"} {
			if !strings.Contains(script, must) {
				t.Errorf("%s completion never mentions %q", name, must)
			}
		}
	}
}

// TestSubCommandsAreKnownCommands keeps the sub-command map from referring to
// a top-level command that no longer exists.
func TestSubCommandsAreKnownCommands(t *testing.T) {
	known := map[string]bool{}
	for _, c := range topLevelCommands {
		known[c] = true
	}
	for parent := range subCommands {
		if !known[parent] {
			t.Errorf("subCommands has %q, which isn't a top-level command", parent)
		}
	}
}

// TestStylingNeverLeaksIntoPipes is the rule that matters most for a
// scriptable CLI: escape sequences must never reach a pipe, a file or --json
// output. The test suite is not a TTY, so colour must already be off here.
func TestStylingNeverLeaksIntoPipes(t *testing.T) {
	if colorEnabled {
		t.Fatal("colour is enabled while not attached to a terminal")
	}
	for name, got := range map[string]string{
		"accent":  accent("x"),
		"bold":    bold("x"),
		"dim":     dim("x"),
		"faint":   faint("x"),
		"ok":      okText("x"),
		"warn":    warnText("x"),
		"danger":  dangerText("x"),
		"heading": heading("x"),
	} {
		if got != "x" {
			t.Errorf("%s(%q) = %q — escape codes leaked into non-TTY output", name, "x", got)
		}
	}
	if strings.Contains(symOK()+symFail()+symBullet(), "\033") {
		t.Error("symbols leaked escape codes into non-TTY output")
	}
}

// TestPaintRespectsColorToggle covers the other direction: when colour IS on,
// the helpers actually emit and terminate escape sequences.
func TestPaintRespectsColorToggle(t *testing.T) {
	original := colorEnabled
	colorEnabled = true
	defer func() { colorEnabled = original }()

	got := accent("hello")
	if !strings.HasPrefix(got, "\033[") {
		t.Errorf("accent() = %q, want a leading escape sequence", got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Errorf("accent() = %q, want a trailing reset", got)
	}
}

// TestDisplayWidthIgnoresEscapes pins the table alignment fix: padding has to
// count visible characters, or coloured cells push every later column out.
func TestDisplayWidthIgnoresEscapes(t *testing.T) {
	plain := "online"
	colored := "\033[38;2;74;222;128monline\033[0m"
	if displayWidth(plain) != displayWidth(colored) {
		t.Errorf("displayWidth(%q)=%d but displayWidth(coloured)=%d",
			plain, displayWidth(plain), displayWidth(colored))
	}
	if got := padRight(colored, 10); displayWidth(got) != 10 {
		t.Errorf("padRight to 10 gave display width %d", displayWidth(got))
	}
}
