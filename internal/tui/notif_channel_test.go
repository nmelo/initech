package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

// isolateHome points the USER settings tier at an empty temp dir so the
// developer's own settings cannot decide the outcome of these tests. Without
// it, anyone who has set preferredNotifChannel would see every injection test
// silently invert -- and it would look like a code failure.
//
// It sets HOME *and* USERPROFILE *and* clears CLAUDE_CONFIG_DIR, because the
// home variable differs per OS. Setting only HOME is what made
// TestNotifChannelArgs_RespectsChannelInUserSettings pass on macOS/Linux and
// FAIL on Windows CI (ini-2fd reopen): on Windows the tier resolves from
// USERPROFILE, so the test relocated nothing and the check read the real
// machine's settings.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func TestNotifChannelArgs_InjectsChannelWhenOperatorSetNone(t *testing.T) {
	_ = isolateHome(t)
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
	_ = isolateHome(t)
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
			_ = isolateHome(t)
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
	home := isolateHome(t)
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
	_ = isolateHome(t)
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
	_ = isolateHome(t)
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
	_ = isolateHome(t)
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
	_ = isolateHome(t)
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

// TestHomeEnvVar_IsTheRightVariablePerOS pins the per-OS resolution on EVERY
// platform, not only the one running the test. This is the assertion that
// would have caught the ini-2fd Windows failure on a macOS developer machine:
// the tier's home variable is a property of the target OS, so it must be
// testable without being on that OS.
func TestHomeEnvVar_IsTheRightVariablePerOS(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "USERPROFILE",
		"darwin":  "HOME",
		"linux":   "HOME",
		"plan9":   "home",
	} {
		if got := homeEnvVar(goos); got != want {
			t.Errorf("homeEnvVar(%q) = %q, want %q -- Claude resolves the user tier from "+
				"CLAUDE_CONFIG_DIR || join(homedir(), \".claude\"), and homedir reads this "+
				"variable; disagreeing means the absence check reads a different file than "+
				"Claude does and fails OPEN over a user-set channel", goos, got, want)
		}
	}
}

// TestManagedSettingsPath_IsTheRightPathPerOS pins the managed/policy path for
// every OS from the same measurement. The Windows arm was missing entirely
// until the ini-2fd reopen, so this exists to keep it from going missing again.
// It asserts EXACT strings, separators included. An earlier version built
// these with filepath.Join and compared prefixes, which passed on macOS and
// Linux and failed on the Windows CI leg with
// "\Library\Application Support\ClaudeCode\..." -- Join uses the separator of
// the platform the test is RUNNING on, not the one the path describes. A
// per-OS path constant has to read the same from every machine or it is not
// pinning anything.
func TestManagedSettingsPath_IsTheRightPathPerOS(t *testing.T) {
	for goos, want := range map[string]string{
		"darwin":  "/Library/Application Support/ClaudeCode/managed-settings.json",
		"windows": `C:\Program Files\ClaudeCode\managed-settings.json`,
		"linux":   "/etc/claude-code/managed-settings.json",
	} {
		if got := managedSettingsPath(goos); got != want {
			t.Errorf("managedSettingsPath(%q) = %q, want %q", goos, got, want)
		}
	}
}

// TestManagedSettingsPath_UsesEachOSsOwnSeparator guards the class rather than
// the three instances: whatever these constants become, a Windows path must
// not carry forward slashes and a POSIX path must not carry backslashes, on
// whichever platform the suite happens to run.
func TestManagedSettingsPath_UsesEachOSsOwnSeparator(t *testing.T) {
	if p := managedSettingsPath("windows"); strings.Contains(p, "/") {
		t.Errorf("windows managed path %q contains a forward slash; it was probably built with "+
			"filepath.Join on a POSIX host", p)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if p := managedSettingsPath(goos); strings.Contains(p, `\`) {
			t.Errorf("%s managed path %q contains a backslash; it was probably built with "+
				"filepath.Join on a Windows host", goos, p)
		}
	}
}

// TestUserConfigDir_HonorsClaudeConfigDir: an operator who relocates their
// Claude config takes the user settings tier with them. Ignoring this variable
// meant reading a settings file the operator does not use, concluding "no
// channel set", and injecting over whatever they had chosen -- a fail-open on
// every platform, not just Windows.
func TestUserConfigDir_HonorsClaudeConfigDir(t *testing.T) {
	isolateHome(t)
	custom := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", custom)

	got, ok := userConfigDir("darwin")
	if !ok || got != custom {
		t.Fatalf("userConfigDir = (%q, %v), want (%q, true); CLAUDE_CONFIG_DIR IS the config "+
			"directory, not the parent of one", got, ok, custom)
	}
}

func TestNotifChannelArgs_RespectsChannelUnderClaudeConfigDir(t *testing.T) {
	isolateHome(t)
	custom := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	if err := os.WriteFile(filepath.Join(custom, "settings.json"),
		[]byte(`{"preferredNotifChannel":"iterm2"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := NotifChannelArgs(t.TempDir(), []string{"claude"}); got != nil {
		t.Errorf("injected %q over a channel set in a relocated CLAUDE_CONFIG_DIR", got)
	}
}

// TestNotifChannelArgs_FailsClosedWhenUserTierUnlocatable is the third
// fail-open hole: when neither CLAUDE_CONFIG_DIR nor the OS home variable is
// set, the user tier cannot be read at all. The first version skipped it
// silently and injected, which is precisely "override a channel we could not
// see". Not finding the tier must count as "they set one".
func TestNotifChannelArgs_FailsClosedWhenUserTierUnlocatable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	if _, ok := userConfigDir(runtime.GOOS); ok {
		t.Fatal("userConfigDir claimed success with no home variable set; the rest of this " +
			"test cannot exercise the unlocatable path")
	}
	if got := NotifChannelArgs(t.TempDir(), []string{"claude"}); got != nil {
		t.Errorf("injected %q while the user settings tier was unlocatable; absence was never "+
			"verified, so this overrides a channel the operator may well have set", got)
	}
}

// TestNotifSettingsPaths_WindowsReadsUserProfileNotHome reproduces the exact
// ini-2fd CI failure from a non-Windows machine, which is the whole point:
// HOME and USERPROFILE point at DIFFERENT directories, and the Windows
// resolution must follow USERPROFILE. A check that followed HOME would look at
// a directory Claude never reads, find no channel, and inject over whatever
// the operator actually set.
func TestNotifSettingsPaths_WindowsReadsUserProfileNotHome(t *testing.T) {
	posixHome := t.TempDir()
	windowsHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", posixHome)
	t.Setenv("USERPROFILE", windowsHome)

	paths, ok := notifSettingsPaths(t.TempDir(), "windows")
	if !ok {
		t.Fatal("windows resolution reported the user tier unlocatable with USERPROFILE set")
	}
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, filepath.Join(windowsHome, ".claude", "settings.json")) {
		t.Errorf("windows resolution never looks under USERPROFILE (%q):\n%s", windowsHome, joined)
	}
	if strings.Contains(joined, filepath.Join(posixHome, ".claude", "settings.json")) {
		t.Errorf("windows resolution reads the POSIX HOME (%q), a directory Claude does not "+
			"consult on Windows:\n%s", posixHome, joined)
	}

	// And the mirror: POSIX resolution must follow HOME, not USERPROFILE.
	posixPaths, ok := notifSettingsPaths(t.TempDir(), "linux")
	if !ok {
		t.Fatal("posix resolution reported the user tier unlocatable with HOME set")
	}
	posixJoined := strings.Join(posixPaths, "\n")
	if !strings.Contains(posixJoined, filepath.Join(posixHome, ".claude", "settings.json")) {
		t.Errorf("posix resolution never looks under HOME (%q):\n%s", posixHome, posixJoined)
	}
}

// TestNotifSettingsPaths_WindowsIncludesTheManagedPolicyFile keeps the Windows
// managed arm present; it was absent entirely before the reopen.
func TestNotifSettingsPaths_WindowsIncludesTheManagedPolicyFile(t *testing.T) {
	isolateHome(t)
	paths, ok := notifSettingsPaths(t.TempDir(), "windows")
	if !ok {
		t.Fatal("windows resolution reported the user tier unlocatable")
	}
	if !strings.Contains(strings.Join(paths, "\n"), `C:\Program Files\ClaudeCode`) {
		t.Errorf("windows resolution omits the managed policy file:\n%s", strings.Join(paths, "\n"))
	}
}
