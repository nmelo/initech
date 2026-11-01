//go:build !windows

package appserve

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// errCopyBusy is a copy lock another process holds past the wait.
var errCopyBusy = errors.New("another initech is putting a copy there")

// lockCopyDir takes the exclusive lock on dir's .copy.lock, waiting up to
// wait for it (0: one try). Every writer of the copy takes it, so install,
// the app service's watcher and a command's first-run refresh never rename
// over the copy at the same moment. The lock is released when the returned
// func runs or the process ends.
func lockCopyDir(dir string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".copy.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing releases it too
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, errCopyBusy
		}
		time.Sleep(20 * time.Millisecond)
	}
}
