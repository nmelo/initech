package tui

import (
	"strings"
	"testing"
)

// ini-47vy: what a message may carry into a pane.

func TestCleanMessage_ThePasteEndSequenceAndWhatFollowsIsText(t *testing.T) {
	got, n := cleanMessage("hello\x1b[201~ls -la\r")
	if got != "hellols -la" || n != 2 {
		t.Fatalf("cleanMessage = %q, %d; want %q, 2 (the sequence counts once, the CR once)", got, n, "hellols -la")
	}
}

func TestCleanMessage_RemovesEachControlShape(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		n              int
	}{
		{"paste start", "a\x1b[200~b", "ab", 1},
		{"bare ESC", "a\x1bb", "ab", 1},
		{"ESC at the end", "a\x1b", "a", 1},
		{"CSI with params", "a\x1b[1;31mb", "ab", 1},
		{"C1 CSI", "a\u009b201~b", "ab", 1},
		{"OSC to BEL", "a\x1b]0;title\x07b", "ab", 1},
		{"OSC to ST", "a\x1b]52;c;aGk=\x1b\\b", "ab", 1},
		{"C1 OSC to C1 ST", "a\u009d0;t\u009cb", "ab", 1},
		{"DCS", "a\x1bPq#0\x1b\\b", "ab", 1},
		{"APC", "a\x1b_x\x1b\\b", "ab", 1},
		{"unterminated OSC keeps its text", "a\x1b]0;title", "a]0;title", 1},
		{"unterminated CSI keeps its text", "a\x1b[12", "a[12", 1},
		{"C0 controls", "a\x00b\x03c\x08d\x15e", "abcde", 4},
		{"CR", "a\rb", "ab", 1},
		{"DEL", "a\x7fb", "ab", 1},
		{"C1 control", "a\u0085b", "ab", 1},
		{"invalid UTF-8", "a\xffb\xc3", "ab", 2},
		{"two sequences", "\x1b[201~x\x1b[200~", "x", 2},
	} {
		got, n := cleanMessage(tc.in)
		if got != tc.want || n != tc.n {
			t.Errorf("%s: cleanMessage(%q) = %q, %d; want %q, %d", tc.name, tc.in, got, n, tc.want, tc.n)
		}
	}
}

// The positive twin: what stays, byte for byte.
func TestCleanMessage_ACleanMessageIsUnchanged(t *testing.T) {
	for _, in := range []string{
		"",
		"hello",
		"line one\nline two\n\tindented",
		"non-ASCII: café, 日本語, emoji 🚀, a literal �",
		"[from eng1] ◆ ini-6if PASS. Brackets [200~] and tildes ~ are text",
		strings.Repeat("long body ", 500),
	} {
		got, n := cleanMessage(in)
		if got != in || n != 0 {
			t.Errorf("cleanMessage(%q) = %q, %d; want it unchanged and 0", in, got, n)
		}
	}
}

func TestCleanMessage_CleaningTwiceRemovesNothingMore(t *testing.T) {
	once, _ := cleanMessage("a\x1b[201~b\x1b]0;t\x07c\x1b")
	twice, n := cleanMessage(once)
	if twice != once || n != 0 {
		t.Fatalf("second clean = %q, %d; want %q, 0", twice, n, once)
	}
}

func TestCleanMessage_OnlyControlsLeavesNothing(t *testing.T) {
	got, n := cleanMessage("\x1b[201~\r\x03")
	if got != "" || n != 3 {
		t.Fatalf("cleanMessage = %q, %d; want empty, 3", got, n)
	}
}
