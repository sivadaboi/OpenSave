//go:build !race

package testutil

// TimeoutScale is 1 for an ordinary run: the waits are already sized for it.
// See timeoutscale_race.go for why an instrumented run needs more.
const TimeoutScale = 1
