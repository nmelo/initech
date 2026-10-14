package roles

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// A generated charter (a role's CLAUDE.md) opens with two comment lines
// (ini-vby5.1):
//
//	<!-- generated from the eng template. initech add-agent eng1 --regen rewrites it; it refuses if this file was edited (--force overrides). -->
//	<!-- initech-hash: sha256:<16 hex> -->
//
// The hash covers everything after the header. It is how add-agent --regen
// tells a charter it may rewrite from one an operator edited by hand: an edit
// below the header changes the body and no longer matches.

const (
	charterHeaderPrefix = "<!-- generated from the "
	charterHashPrefix   = "<!-- initech-hash: sha256:"
	charterCommentEnd   = " -->"
)

// CharterHeader returns the two-line generated header for a charter rendered
// from template tmplName for role, whose body (everything after the header)
// is body.
func CharterHeader(tmplName, role, body string) string {
	return charterHeaderPrefix + tmplName + " template. initech add-agent " + role + " --regen rewrites it; it refuses if this file was edited (--force overrides)." + charterCommentEnd + "\n" +
		charterHashPrefix + charterBodyHash(body) + charterCommentEnd + "\n"
}

// ParseCharter splits a charter into its body and reports whether it carries
// a generated header and whether the body still matches the header's hash.
// A file without the header returns hasHeader=false and the whole text as
// body.
func ParseCharter(text string) (body string, hasHeader, unedited bool) {
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || !strings.HasPrefix(first, charterHeaderPrefix) {
		return text, false, false
	}
	second, body, ok := strings.Cut(rest, "\n")
	if !ok || !strings.HasPrefix(second, charterHashPrefix) || !strings.HasSuffix(second, charterCommentEnd) {
		return text, false, false
	}
	want := strings.TrimSuffix(strings.TrimPrefix(second, charterHashPrefix), charterCommentEnd)
	return body, true, want == charterBodyHash(body)
}

func charterBodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}
