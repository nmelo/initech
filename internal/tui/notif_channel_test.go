package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSettings writes a settings file at dir/.claude/<name>.
func writeSettings(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// isolateHome points HOME at an empty temp dir so the developer's own
// ~/.claude/settings.json cannot decide the outcome of these tests. Without
// it, anyone who has set preferredNotifChannel would see every injection test
// silently invert -- and it would look like a code failure.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestNotifChannelArgs_InjectsChannelWhenOperatorSetNone(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	got := NotifChannelArgs(dir, []string{"claude", "--continue"})

	if len(got) != 2 || got[0] != "--settings" {
		t.Fatalf("expected a --settings pair, got %q", got)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(got[1]), &parsed); err != nil {
		t.Fatalf("injected blob is not valid JSON (%v): %s", err, got[1])
	}
	if parsed[notifChannelSettingKey] != pinnedTermProgram {
		t.Errorf("injected channel = %q, want the pinned identity %q -- the channel and the "+
			"pinned TERM_PROGRAM are the same claim and must not drift",
			parsed[notifChannelSettingKey], pinnedTermProgram)
	}
}

// TestNotifChannelArgs_StandsDownWhenOperatorPassesSettings pins the measured
// composition rule: two --settings flags do not merge, the later replaces the
// earlier wholesale. Appending ours would discard the operator's entire blob.
func TestNotifChannelArgs_StandsDownWhenOperatorPassesSettings(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()

	for _, args := range [][]string{
		{"claude", "--settings", `{"permissions":{"allow":[]}}`},
		{"claude", `--settings={"permissions":{"allow":[]}}`},
		{"claude", "--settings", "/etc/some/settings.json"},
	} {
		if got := NotifChannelArgs(dir, args); got != nil {
			t.Errorf("args %q: injected %q; a second --settings would replace the operator's "+
				"blob wholesale, taking their permissions and hooks with it", args, got)
		}
	}
}

func TestNotifChannelArgs_RespectsChannelInEverySettingsTier(t *testing.T) {
	tiers := []struct {
		name string
		file string
	}{
		{"project settings", "settings.json"},
		{"project local settings", "settings.local.json"},
	}
	for _, tier := range tiers {
		t.Run(tier.name, func(t *testing.T) {
			isolateHome(t)
			dir := t.TempDir()
			writeSettings(t, dir, tier.file, `{"preferredNotifChannel":"terminal_bell"}`)

			if got := NotifChannelArgs(dir, []string{"claude"}); got != nil {
				t.Errorf("injected %q over an explicit operator channel in %s; an explicit "+
					"user value always wins, even at the cost of a dead chime", got, tier.file)
			}
		})
	}
}

// TestNotifChannelArgs_RespectsChannelInUserSettings covers the user tier,
// which the live rig could not reach: redirecting HOME to exercise
// ~/.claude/settings.json also breaks the child's auth, so the live cell was
// inconclusive by its own control. The tier is covered here instead.
func TestNotifChannelArgs_RespectsChannelInUserSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSettings(t, home, "settings.json", `{"preferredNotifChannel":"iterm2"}`)

	if got := NotifChannelArgs(t.TempDir(), []string{"claude"}); got != nil {
		t.Errorf("injected %q over a channel set in the USER settings tier: %q", got, home)
	}
}

// TestNotifChannelArgs_RespectsChannelInAnAncestorProject pins the reason the
// search walks upward: agents run in <root>/<role>, while the repo's settings
// file lives at <root>. Checking only the working directory would miss the
// settings of the very repo the agent is working in.
func TestNotifChannelArgs_RespectsChannelInAnAncestorProject(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	writeSettings(t, root, "settings.json", `{"preferredNotifChannel":"kitty"}`)
	role := filepath.Join(root, "eng1")
	if err := os.MkdirAll(role, 0o755); err != nil {
		t.Fatalf("mkdir role: %v", err)
	}

	if got := NotifChannelArgs(role, []string{"claude"}); got != nil {
		t.Errorf("injected %q while an ancestor project set a channel", got)
	}
}

// TestNotifChannelArgs_FailsClosedOnUnreadableSettings pins the asymmetry:
// wrongly injecting overrides a deliberate preference, wrongly declining costs
// a chime. A file we cannot parse must be read as "the operator set something".
func TestNotifChannelArgs_FailsClosedOnUnreadableSettings(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	writeSettings(t, dir, "settings.json", `{"preferredNotifChannel": TRUNCATED`)

	if got := NotifChannelArgs(dir, []string{"claude"}); got != nil {
		t.Errorf("injected %q despite an unparseable settings file; a file we cannot read "+
			"must not be treated as consent to override", got)
	}
}

// TestNotifChannelArgs_InjectsWhenSettingsExistWithoutAChannel is the other
// direction of the same guard: a settings file the operator uses for
// unrelated keys must not be mistaken for a channel choice, or the fix would
// silently skip most real projects.
func TestNotifChannelArgs_InjectsWhenSettingsExistWithoutAChannel(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	writeSettings(t, dir, "settings.json", `{"permissions":{"ask":["Bash"]}}`)

	if got := NotifChannelArgs(dir, []string{"claude"}); len(got) == 0 {
		t.Error("declined to inject over a settings file that sets no channel at all")
	}
}

// TestNotifChannelArgs_IgnoresEmptyChannelValue: a key present but blank is
// not a choice, and treating it as one would strand the operator with the
// dead chime this bead exists to fix.
func TestNotifChannelArgs_IgnoresEmptyChannelValue(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	writeSettings(t, dir, "settings.json", `{"preferredNotifChannel":"  "}`)

	if got := NotifChannelArgs(dir, []string{"claude"}); len(got) == 0 {
		t.Error("treated a blank preferredNotifChannel as an explicit operator choice")
	}
}

// TestNotifChannelSettings_MatchesTheMeasuredFlagShape pins the exact blob the
// live probe measured. If this and the probe's injectedChannelArgs disagree,
// the matrix is measuring something the product does not ship.
func TestNotifChannelSettings_MatchesTheMeasuredFlagShape(t *testing.T) {
	got := notifChannelSettings()
	want := `{"preferredNotifChannel":"ghostty"}`
	if got != want {
		t.Errorf("injected blob = %s, want %s (the shape measured in the live matrix)", got, want)
	}
	if !strings.Contains(got, pinnedTermProgram) {
		t.Errorf("blob %s does not carry the pinned identity %q", got, pinnedTermProgram)
	}
}
