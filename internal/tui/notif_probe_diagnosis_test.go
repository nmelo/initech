package tui

// notif_probe_diagnosis_test.go holds the PORTABLE half of the ini-2fd live
// probe: reading the emulator screen, dumping raw output, and diagnosing WHY a
// cell was silent. None of it needs a PTY, so none of it carries the probe
// file's !windows constraint -- the ini-47w rule that a portable symbol parked
// in a tagged file drags its whole suite off a platform nobody chose to drop.
//
// silentDiagnosis in particular earns portable tests: its first version keyed
// the dialog check on "Claude needs your permission", which is the
// NOTIFICATION's body and therefore can never appear in a silent cell. The
// branch was unreachable by construction and misfiled every genuine silence as
// a broken precondition, which in turn would have made the shadow-marker
// rescue test vacuous.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// renderedScreen returns the emulator's visible rows joined by newlines --
// what a human would SEE, with word positioning resolved.
func renderedScreen(emu *vt.SafeEmulator) string {
	var rows []string
	for y := 0; y < 40; y++ {
		rows = append(rows, strings.TrimRight(emu.RowText(y, 120), " "))
	}
	return strings.Join(rows, "\n")
}

// dumpProbeOutput writes a cell's raw PTY bytes to $INITECH_NOTIF_PROBE_DUMP
// when set, so an unexpected silence can be read rather than guessed at.
func dumpProbeOutput(t *testing.T, out string) {
	t.Helper()
	dir := os.Getenv("INITECH_NOTIF_PROBE_DUMP")
	if dir == "" {
		return
	}
	name := filepath.Join(dir, strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())+".raw")
	if err := os.WriteFile(name, []byte(out), 0o644); err != nil {
		t.Logf("dump failed: %v", err)
		return
	}
	t.Logf("raw PTY output dumped to %s (%d bytes)", name, len(out))
}

func silentDiagnosis(out string) string {
	// The dialog test comes FIRST: reaching the dialog is the precondition,
	// and it is positive evidence that outranks any keyword the session may
	// also have printed.
	//
	// It keys on the RENDERED dialog ("Do you want to proceed?"), NOT on
	// "Claude needs your permission". That phrase is the NOTIFICATION's body,
	// so a silent cell can never contain it -- keying on it made this branch
	// unreachable by construction and misfiled every real silence as a broken
	// precondition. A guard phrased in terms of the thing it is testing for
	// cannot fire.
	//
	// The unauthenticated pattern is likewise narrowed to actual login
	// prompts: a bare /authenticat/ matched the routine startup line
	// "1 MCP server needs authentication", which is not an auth failure.
	switch {
	case regexp.MustCompile(`Do you want to proceed\?|requires confirmation for this command`).MatchString(out):
		return "SILENT/dialog-reached: the dialog appeared and NO notification followed -- a real measured silence"
	case out == "":
		return "SILENT/no-output: child produced nothing (did it start?)"
	case regexp.MustCompile(`(?i)welcome to claude|let's get started|choose.*theme`).MatchString(out):
		return "SILENT/onboarding: session sat in first-run onboarding -- this cell measures NOTHING about channel resolution"
	case regexp.MustCompile(`(?i)please (log|sign) ?in|run /login|not authenticated|invalid api key`).MatchString(out):
		return "SILENT/unauthenticated: session never reached a dialog -- this cell measures NOTHING about channel resolution"
	default:
		return "SILENT/dialog-not-reached: no permission dialog in the output -- precondition unmet, cell inconclusive"
	}
}

// TestSilentDiagnosis_ClassifiesEachSilence pins the diagnosis branches. A
// silent cell is only evidence about channel resolution if its session
// actually reached the dialog; every other silence means the cell measured
// nothing, and mislabelling one as the other is how a broken rig reads as a
// finding.
func TestSilentDiagnosis_ClassifiesEachSilence(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   string
	}{
		{
			// The rendered dialog, as the emulator resolves it. This is the
			// ONLY silence that measures anything.
			name:   "dialog reached",
			screen: "Bash command\ndate +%s\nDo you want to proceed?\n1. Yes\n2. No",
			want:   "dialog-reached",
		},
		{
			// Regression: the routine startup line "1 MCP server needs
			// authentication" is not an auth failure, and a bare /authenticat/
			// pattern classified a perfectly good baseline run as unauthenticated.
			name:   "mcp auth notice alongside the dialog",
			screen: "1 MCP server needs authentication - run /mcp\nDo you want to proceed?",
			want:   "dialog-reached",
		},
		{
			name:   "real login prompt",
			screen: "Please log in to continue",
			want:   "unauthenticated",
		},
		{
			name:   "first run onboarding",
			screen: "Welcome to Claude Code\nChoose your theme",
			want:   "onboarding",
		},
		{name: "no output at all", screen: "", want: "no-output"},
		{
			name:   "ran but never prompted",
			screen: "some ordinary session output with no dialog",
			want:   "dialog-not-reached",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := silentDiagnosis(c.screen)
			if !strings.Contains(got, c.want) {
				t.Errorf("silentDiagnosis(%q)\n  = %q\n want it to classify as %q", c.screen, got, c.want)
			}
		})
	}
}

// TestSilentDiagnosis_DialogTextIsNotTheNotificationBody guards the specific
// bug directly: keying the precondition check on the notification's own text
// makes the branch unreachable, because a silent cell by definition never
// emitted a notification.
func TestSilentDiagnosis_DialogTextIsNotTheNotificationBody(t *testing.T) {
	got := silentDiagnosis("Claude needs your permission")
	if strings.Contains(got, "dialog-reached") {
		t.Error("classified the NOTIFICATION body as evidence the dialog was reached; " +
			"that string only exists when the cell was not silent, so the check cannot fire")
	}
}

// TestRenderedScreen_ResolvesWordPositionedText pins why the diagnosis reads
// the emulator rather than the byte stream: Claude positions words with cursor
// moves, so the dialog phrase is not contiguous in the raw PTY output.
func TestRenderedScreen_ResolvesWordPositionedText(t *testing.T) {
	emu := vt.NewSafeEmulator(120, 40)
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	// "Do you want to proceed?" written as separately positioned words, the
	// shape captured from a real session.
	raw := "\x1b[1;1HDo\x1b[1;4Hyou\x1b[1;8Hwant\x1b[1;13Hto\x1b[1;16Hproceed?"
	emu.Write([]byte(raw))

	if strings.Contains(raw, "Do you want to proceed?") {
		t.Fatal("the fixture is contiguous in raw bytes, so it cannot demonstrate the trap")
	}
	screen := renderedScreen(emu)
	if !strings.Contains(screen, "Do you want to proceed?") {
		t.Errorf("rendered screen did not resolve the positioned words: %q", firstLine(screen))
	}
	if !strings.Contains(silentDiagnosis(screen), "dialog-reached") {
		t.Error("diagnosis missed the dialog on a rendered screen that plainly shows it")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
