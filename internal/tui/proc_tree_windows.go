//go:build windows

package tui

import "errors"

// readProcTable is not available on Windows: background work there is the
// footer's reading, labelled as seen for (ini-d2sx).
func readProcTable() (string, error) {
	return "", errors.New("process table not read on Windows")
}
