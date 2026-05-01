//go:build windows

package tui

// redirectStderr is a no-op on Windows. The dup2-style fd redirection used on
// Unix doesn't translate cleanly to Windows handles, and for the wintest-win
// use case we don't need crash-capture-to-log.
func redirectStderr(projectRoot string) func() {
	return func() {}
}
