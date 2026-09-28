//go:build race

package ecma262_test

// raceEnabled reports whether the race detector is on; it slows matching by an
// order of magnitude, which wall-clock guards must not mistake for a regression.
const raceEnabled = true
