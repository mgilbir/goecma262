package vm

// SetOptimize switches the analysis-driven shortcuts on or off for Programs
// built afterwards, returning a func that restores the previous setting.
// Tests use the unoptimised engine as a reference implementation.
func SetOptimize(on bool) (restore func()) {
	old := optimize
	optimize = on
	return func() { optimize = old }
}
