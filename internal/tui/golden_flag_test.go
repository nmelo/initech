package tui

import "flag"

// updateGoldenFlag regenerates golden files instead of comparing against them.
// Deliberately a flag, not an env var: regenerating a golden is an assertion
// that the change is intended, and it should have to be typed.
var updateGoldenFlag = flag.Bool("update-golden", false, "rewrite golden files instead of comparing")

func updateGolden() bool { return *updateGoldenFlag }
