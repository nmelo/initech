package cmd

import (
	"runtime"
	"testing"
)

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix-specific features (sockets, /tmp, /bin/sh)")
	}
}
