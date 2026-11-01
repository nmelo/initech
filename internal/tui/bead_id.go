package tui

import (
	"regexp"
	"strings"
)

// storedBeadIDRe is a bd id: the prefix, a dash, the key (a molecule's key may
// carry more dashes), and an optional dotted child number per level.
var storedBeadIDRe = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)+(?:\.[0-9]+)*$`)

// IsBeadID reports whether s has a bd id's shape. Every bead value initech
// stores, sends or records passes it; a title never does.
func IsBeadID(s string) bool { return storedBeadIDRe.MatchString(s) }

// CutBeadID returns s's id: s itself when it is one, else its first
// tab-separated field when that is one (a pasted bd row, or a value stored
// with its title joined on), else "".
func CutBeadID(s string) string {
	if IsBeadID(s) {
		return s
	}
	if i := strings.IndexByte(s, '\t'); i >= 0 && IsBeadID(s[:i]) {
		return s[:i]
	}
	return ""
}
