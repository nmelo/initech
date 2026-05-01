package tui

import "runtime"

// modKey is the platform-appropriate label for the Alt/Option modifier key.
// macOS keyboards label this key "Option"; all other platforms use "Alt".
// Set once at package init; used by all shortcut label rendering.
var modKey = "Alt"

func init() {
	if runtime.GOOS == "darwin" {
		modKey = "Opt"
	}
}
