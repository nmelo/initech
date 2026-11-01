//go:build windows

package appserve

import (
	"errors"
	"time"
)

// errCopyBusy is a copy lock another process holds past the wait.
var errCopyBusy = errors.New("another initech is putting a copy there")

// lockCopyDir does nothing on Windows: the copy launchd runs is a macOS
// thing.
func lockCopyDir(dir string, wait time.Duration) (func(), error) {
	return func() {}, nil
}
