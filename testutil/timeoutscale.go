//go:build !race

package testutil

// TimeoutScale is 1 for an ordinary run: the waits are already sized for it.
// See timeoutscale_race.go for why an instrumented run needs more.
const TimeoutScale = 1

// SettleScale stretches the fixed pauses tests use to let background work
// finish — a watcher debounce, an auto-sync round — as opposed to the waits
// that poll for a condition.
//
// Those pauses were written against an idle machine at ordinary speed, and
// they are the one timing knob that never scaled. A polling wait that is too
// short reports a clear failure; a settle pause that is too short lets the
// NEXT step start while the previous one is still running, which shows up as
// an unrelated assertion failing somewhere later. That is the shape of every
// load flake seen in this suite.
//
// Separate from TimeoutScale, and smaller, because these are unconditional
// sleeps: there are around ninety of them, so a factor of six would add
// twenty minutes of doing nothing to a race run.
const SettleScale = 1
