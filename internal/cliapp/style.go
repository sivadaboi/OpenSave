package cliapp

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// Terminal styling built on OpenSave's palette — purple accent on a dark
// background — with two hard rules:
//
//   1. Nothing is emitted when output isn't a terminal. Piping to jq, a file
//      or a log must produce clean text, never escape sequences.
//   2. NO_COLOR is honoured (https://no-color.org).
//
// Colours are truecolor with a 256-colour fallback, matching the app's
// --accent (#8a63f4) rather than a generic ANSI magenta.

const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"

	// #8a63f4 — the app's accent.
	ansiAccent = "\033[38;2;138;99;244m"
	// Lighter accent for headings.
	ansiAccentBright = "\033[38;2;155;122;247m"
	ansiSuccess      = "\033[38;2;74;222;128m"  // #4ade80
	ansiWarn         = "\033[38;2;251;191;36m"  // #fbbf24
	ansiDanger       = "\033[38;2;217;87;87m"   // #d95757
	ansiFaint        = "\033[38;2;122;122;133m" // muted text
)

// colorEnabled is resolved once at startup.
var colorEnabled = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	// Not a terminal (piped or redirected) — emit plain text.
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return enableVirtualTerminal()
}

func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func accent(s string) string  { return paint(ansiAccent, s) }
func heading(s string) string { return paint(ansiBold+ansiAccentBright, s) }
func bold(s string) string    { return paint(ansiBold, s) }
func dim(s string) string     { return paint(ansiDim, s) }
func faint(s string) string   { return paint(ansiFaint, s) }
func okText(s string) string  { return paint(ansiSuccess, s) }
func warnText(s string) string { return paint(ansiWarn, s) }
func dangerText(s string) string { return paint(ansiDanger, s) }

// Symbols degrade to ASCII when colour is off, since a terminal that can't do
// colour often can't do box-drawing either.
func sym(fancy, plain string) string {
	if colorEnabled {
		return fancy
	}
	return plain
}

func symOK() string    { return okText(sym("✓", "[ok]")) }
func symFail() string  { return dangerText(sym("✗", "[!]")) }
func symBullet() string { return accent(sym("▸", ">")) }
func symDot() string   { return sym("·", "-") }

// ── Output blocks ────────────────────────────────────────────────────────

// section prints a titled block header, e.g.
//
//	OpenSave · Devices
//	──────────────────
func section(title string) {
	fmt.Println()
	fmt.Println(heading(title))
	fmt.Println(faint(strings.Repeat(sym("─", "-"), displayWidth(title))))
}

// field prints an aligned "label  value" line for detail views.
func field(label, value string) {
	fmt.Printf("  %s  %s\n", faint(padRight(label+":", 16)), value)
}

// note prints secondary guidance under a result.
func note(s string) { fmt.Println(faint("  " + s)) }

// success, failure and warning print a one-line result.
func success(format string, a ...any) {
	fmt.Printf("%s %s\n", symOK(), fmt.Sprintf(format, a...))
}
func warning(format string, a ...any) {
	fmt.Printf("%s %s\n", warnText(sym("!", "[warn]")), fmt.Sprintf(format, a...))
}

// hint prints a suggested next command, indented and dimmed.
func hint(lines ...string) {
	fmt.Println()
	for _, l := range lines {
		fmt.Printf("  %s %s\n", faint(sym("→", "->")), dim(l))
	}
}

// ── Tables ───────────────────────────────────────────────────────────────

// table renders aligned rows without borders — closer to `git` or `kubectl`
// than to an ASCII-art grid, which wraps badly in a narrow terminal.
type table struct {
	headers []string
	rows    [][]string
}

func newTable(headers ...string) *table { return &table{headers: headers} }

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) render() {
	if len(t.rows) == 0 {
		return
	}
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = displayWidth(h)
	}
	for _, row := range t.rows {
		for i, c := range row {
			if i < len(widths) && displayWidth(c) > widths[i] {
				widths[i] = displayWidth(c)
			}
		}
	}

	var head strings.Builder
	head.WriteString("  ")
	for i, h := range t.headers {
		head.WriteString(padRight(strings.ToUpper(h), widths[i]+2))
	}
	fmt.Println(faint(strings.TrimRight(head.String(), " ")))

	for _, row := range t.rows {
		fmt.Print("  ")
		for i, c := range row {
			if i >= len(widths) {
				continue
			}
			// Pad on the *display* width so colour codes don't skew alignment.
			fmt.Print(padRight(c, widths[i]+2))
		}
		fmt.Println()
	}
}

// padRight pads to a display width, ignoring ANSI escapes so coloured cells
// still line up.
func padRight(s string, width int) string {
	pad := width - displayWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// displayWidth counts visible runes, skipping ANSI escape sequences.
func displayWidth(s string) int {
	n, inEscape := 0, false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
		default:
			n++
		}
	}
	if n == 0 {
		return utf8.RuneCountInString(s)
	}
	return n
}
