//go:build race

package testutil

// TimeoutScale multiplies every wait in the harness when the race detector is
// on.
//
// The detector instruments every memory access, costing something like five to
// twenty times the runtime, and CI runs the whole suite under it alongside
// another package. These are end-to-end tests — real daemons, real HTTP, real
// file watching — so that slowdown lands squarely on the waits, and they were
// all written against ordinary speed. The symptom was a different timing test
// failing on almost every run, each one passing on its own in well under a
// second and then timing out after sixty under load.
//
// Scaling the waits gives the same assertions more wall-clock room. It never
// makes a test pass that would otherwise fail: a condition that is never going
// to hold still fails, just later.
const TimeoutScale = 6
