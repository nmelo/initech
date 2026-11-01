//go:build !linux && !darwin

package tui

import (
	"errors"
	"os"
	"runtime"
)

// slaveRawMode: no termios to read here.
func slaveRawMode(*os.File) (bool, error) {
	return platformDefaultRawMode()
}

// platformDefaultRawMode: Windows has no termios and its ConPTY carries no
// XOFF, so the stash key is typed as it always was. Any other platform is not
// a release target and reads as unknown, so it types no stash.
func platformDefaultRawMode() (bool, error) {
	if runtime.GOOS == "windows" {
		return true, nil
	}
	return false, errors.New("terminal mode cannot be read on " + runtime.GOOS)
}
