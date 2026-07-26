package cliapp

import (
	"fmt"
	"strings"
)

// The OpenSave wordmark. Shown at the top of the status panel when the
// terminal is wide enough to render it without wrapping — a banner that wraps
// looks broken, so anything narrower falls back to a plain title line.
//
// Needs 87 columns and a UTF-8 terminal; both are checked before printing.
const bannerWidth = 87

var banner = []string{
	`    ███████                                   █████████`,
	`  ███░░░░░███                                ███░░░░░███`,
	` ███     ░░███ ████████   ██████  ████████  ░███    ░░░   ██████   █████ █████  ██████`,
	`░███      ░███░░███░░███ ███░░███░░███░░███ ░░█████████  ░░░░░███ ░░███ ░░███  ███░░███`,
	`░███      ░███ ░███ ░███░███████  ░███ ░███  ░░░░░░░░███  ███████  ░███  ░███ ░███████`,
	`░░███     ███  ░███ ░███░███░░░   ░███ ░███  ███    ░███ ███░░███  ░░███ ███  ░███░░░`,
	` ░░░███████░   ░███████ ░░██████  ████ █████░░█████████ ░░████████  ░░█████   ░░██████`,
	`   ░░░░░░░     ░███░░░   ░░░░░░  ░░░░ ░░░░░  ░░░░░░░░░   ░░░░░░░░    ░░░░░     ░░░░░░`,
	`               ░███`,
	`               █████`,
	`              ░░░░░`,
}

// showBanner reports whether the wordmark will render properly here. It needs
// a real terminal (so styling is on and the encoding is known good) and enough
// columns; piped output and narrow windows get the plain title instead.
func showBanner() bool {
	if !colorEnabled {
		return false
	}
	w := terminalWidth()
	return w == 0 || w >= bannerWidth+2 // 0 means "couldn't tell" — assume roomy
}

// bannerSplit is the column where "Open" ends and "Save" begins. The glyphs
// interlock — the "n" runs to column 43 and the "S" shadow starts at 44 — so
// this is the only column that cleanly divides every row.
const bannerSplit = 44

// printBanner draws the wordmark the way the logo does it: "Open" in the text
// colour, "Save" in the accent purple.
func printBanner() {
	for _, line := range banner {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// The descender rows of the "p" are shorter than the split and belong
		// entirely to "Open".
		if len([]rune(line)) <= bannerSplit {
			fmt.Println(white(line))
			continue
		}
		runes := []rune(line)
		fmt.Println(white(string(runes[:bannerSplit])) + accent(string(runes[bannerSplit:])))
	}
}
