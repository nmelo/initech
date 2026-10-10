//go:build windows

package main

import (
	"fmt"
	"os"
)

// The rig drives initech through a Unix PTY; there is no Windows driver.
func main() {
	fmt.Fprintln(os.Stderr, "rig: needs a Unix PTY; run it on macOS or Linux")
	os.Exit(2)
}
