package appserve

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// logMode is the service log's mode: readable by its owner alone.
const logMode os.FileMode = 0o600

// EnsurePrivateLog makes the log at path ready for launchd to append to:
// its folder (0700 when it has to be made; an existing folder's mode is
// left alone) and the file created 0600. launchd's StandardOutPath cannot
// set a mode, so the file must exist before the job starts. An existing log
// with any group or world bit is narrowed to 0600, and narrowed says so.
// A log that cannot be opened for writing is an error.
func EnsurePrivateLog(path string) (narrowed bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logMode)
	if err != nil {
		return false, err
	}
	defer f.Close()
	return narrow(f)
}

// NarrowLog narrows the service's own log when launchd hands it one that is
// wider than 0600 (a file made before EnsurePrivateLog existed): f is the
// service's stderr, and only a regular file is touched. One line on log says
// it had to; a terminal, a pipe or a 0600 file are left alone, silently.
func NarrowLog(f *os.File, log io.Writer) {
	if ok, err := narrow(f); ok && err == nil {
		fmt.Fprintln(log, "initech app-serve: its log was readable by others; it is now 0600")
	}
}

// narrow sets an open regular file to logMode if it has any other bit.
func narrow(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() == logMode {
		return false, err
	}
	if err := f.Chmod(logMode); err != nil {
		return false, err
	}
	return true, nil
}
