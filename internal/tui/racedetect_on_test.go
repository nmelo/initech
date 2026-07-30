//go:build race

package tui

// raceDetectorEnabled is true when the test binary is built with -race.
// Used to skip tight wall-clock "completed fast enough" assertions that
// -race's own 5-10x instrumentation overhead can push past their deadline
// under load, without weakening the deadline itself (ini-ls0c/ini-adb9).
const raceDetectorEnabled = true
