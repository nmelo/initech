//go:build !windows

package tui

// notif_channel_probe_test.go is the LIVE measurement rig for ini-2fd: it
// spawns a real Claude Code, drives it into a real dialog, and reports WHICH
// notification channel actually fired.
//
// The observable is the emitted OSC sequence, not anything Claude reports
// about its own configuration. That is deliberate: the question this rig
// answers is a precedence question about another program's settings
// resolution, and reading its resolution order out of a binary or a doc is
// exactly the modelling-instead-of-measuring failure the attention family
// keeps re-learning. Which byte comes out of the PTY is ground truth and stays
// true across resolution-order changes we are not told about.
//
//	ghostty -> OSC 777   iterm2 -> OSC 9   kitty -> OSC 99
//	terminal_bell -> BEL   notifications_disabled / unresolvable -> nothing
//
// SAFETY: every settings file this rig writes lives in a t.TempDir() project.
// The operator's ~/.claude is never read for mutation and never written --
// same standing property as scripts/sigcapture (see its header).
//
// Gated behind INITECH_NOTIF_PROBE=1 because each cell costs a real session.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// notifCell is one measurement: a settings arrangement plus the environment
// markers to apply, run to whichever notification channel actually fires.
type notifCell struct {
	// projectSettings is written to <tmp>/.claude/settings.json. It always
	// carries the permission-ask that forces the dialog; the channel is
	// merged in by the runner when projectChannel is set.
	projectChannel string
	// localChannel, when set, is written to <tmp>/.claude/settings.local.json.
	localChannel string
	// redirectHome points HOME at a throwaway dir with no user settings.
	// Redirecting HOME is how the user settings tier WOULD be exercised
	// without touching the operator's real ~/.claude -- but it also breaks
	// the child's auth, which is a measured limit rather than an assumption
	// (see TestNotifChannel_UserSettingsTierIsNotLiveTestable).
	redirectHome bool
	// settingsArgs are the --settings flags in argv order, operator's first.
	settingsArgs []string
	// env are extra KEY=VALUE markers applied on top of agentBaseEnv().
	env []string
}

// notifResult is which channel was observed, as a channel name.
type notifResult struct {
	channel string // "ghostty" | "iterm2" | "kitty" | "silent"
	payload string
	elapsed time.Duration
}

var (
	osc777Re = regexp.MustCompile(`\x1b\]777;notify;[^\x07]*\x07`)
	osc9Re   = regexp.MustCompile(`\x1b\]9;[^\x07]*\x07`)
	osc99Re  = regexp.MustCompile(`\x1b\]99;[^\x07]*\x07`)
)

// runNotifCell spawns a real Claude under a PTY and returns the first
// notification channel observed, or "silent" if none arrived before deadline.
func runNotifCell(t *testing.T, cell notifCell, deadline time.Duration) notifResult {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Force an approval prompt regardless of the operator's own allowlist,
	// which would otherwise auto-approve the dialog we came to observe.
	settings := `{"permissions":{"ask":["Bash"],"allow":[],"deny":[]}}`
	if cell.projectChannel != "" {
		settings = `{"permissions":{"ask":["Bash"],"allow":[],"deny":[]},` +
			`"preferredNotifChannel":"` + cell.projectChannel + `"}`
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if cell.localChannel != "" {
		local := `{"preferredNotifChannel":"` + cell.localChannel + `"}`
		if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.local.json"), []byte(local), 0o644); err != nil {
			t.Fatalf("write local settings: %v", err)
		}
	}
	var homeEnv []string
	if cell.redirectHome {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
			t.Fatalf("mkdir home: %v", err)
		}
		homeEnv = []string{"HOME=" + home}
	}

	argv := append([]string{}, cell.settingsArgs...)
	argv = append(argv, "Run the shell command: date +%s")
	cmd := exec.Command("claude", argv...)
	cmd.Dir = dir
	// agentBaseEnv, not os.Environ(): measure the environment production
	// actually produces (the g0h rule the emitter canary already follows).
	cmd.Env = append(agentBaseEnv(),
		"CLAUDE_CODE_CHILD_SESSION=",
		"CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1",
	)
	cmd.Env = append(cmd.Env, homeEnv...)
	cmd.Env = append(cmd.Env, cell.env...)

	start := time.Now()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start claude under pty: %v", err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	chunks := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				chunks <- cp
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()

	// The dialog text is read through the emulator, not off the raw stream.
	// Claude positions words separately, so "Do you want to proceed?" is NOT
	// contiguous in the PTY bytes (it arrives as Doyouwanttoproceed? with the
	// spacing carried by cursor moves) -- the same trap sigcapture's header
	// records for codex, and it silently turned every raw-byte dialog test
	// into a false negative here.
	emu := vt.NewSafeEmulator(120, 40)
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	var seen strings.Builder
	trustRe := regexp.MustCompile(`(?i)trust`)
	trusted := false
	timer := time.After(deadline)
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				dumpProbeOutput(t, seen.String())
				return notifResult{channel: "silent", elapsed: time.Since(start), payload: silentDiagnosis(renderedScreen(emu))}
			}
			seen.Write(c)
			emu.Write(c)
			s := seen.String()
			if !trusted && trustRe.MatchString(s) {
				trusted = true
				time.Sleep(500 * time.Millisecond)
				_, _ = ptmx.Write([]byte("\r"))
			}
			if m := osc777Re.FindString(s); m != "" {
				return notifResult{channel: "ghostty", payload: m, elapsed: time.Since(start)}
			}
			if m := osc9Re.FindString(s); m != "" {
				return notifResult{channel: "iterm2", payload: m, elapsed: time.Since(start)}
			}
			if m := osc99Re.FindString(s); m != "" {
				return notifResult{channel: "kitty", payload: m, elapsed: time.Since(start)}
			}
		case <-timer:
			dumpProbeOutput(t, seen.String())
			return notifResult{channel: "silent", elapsed: time.Since(start), payload: silentDiagnosis(renderedScreen(emu))}
		}
	}
}

func requireNotifProbe(t *testing.T) {
	t.Helper()
	if os.Getenv("INITECH_NOTIF_PROBE") != "1" {
		t.Skip("set INITECH_NOTIF_PROBE=1 to run the live notification-channel probe")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}
}

// TestNotifChannel_SettingsPrecedenceHoldsAsMeasured pins the ini-2fd
// precedence matrix as a GUARD, not a log. Every row here is load-bearing for
// the shipped design, so each one asserts: if Claude's settings resolution
// changes under us, this goes red and names which assumption died rather than
// leaving the injection quietly wrong.
func TestNotifChannel_SettingsPrecedenceHoldsAsMeasured(t *testing.T) {
	requireNotifProbe(t)

	ghosttyFlag := []string{"--settings", `{"preferredNotifChannel":"ghostty"}`}
	iterm2Flag := []string{"--settings", `{"preferredNotifChannel":"iterm2"}`}

	cells := []struct {
		name string
		cell notifCell
		want string
		why  string
	}{
		{
			name: "A settings file alone is honored",
			cell: notifCell{projectChannel: "iterm2"},
			want: "iterm2",
			why:  "a channel in .claude/settings.json must drive the emitted channel; if not, the observable itself is broken and every other row is meaningless",
		},
		{
			name: "B the flag outranks the operator's settings file",
			cell: notifCell{projectChannel: "iterm2", settingsArgs: ghosttyFlag},
			want: "ghostty",
			why:  "THE LOAD-BEARING ROW. --settings beating the operator's own file is why initech must check for an existing channel itself: Claude will not protect a deliberate user choice from our injection",
		},
		{
			name: "C the flag alone selects the channel",
			cell: notifCell{settingsArgs: ghosttyFlag},
			want: "ghostty",
			why:  "the injection's basic claim",
		},
		{
			name: "D of two flags the later wins",
			cell: notifCell{settingsArgs: append(append([]string{}, iterm2Flag...), ghosttyFlag...)},
			want: "ghostty",
			why:  "multiple --settings resolve last-wins rather than erroring",
		},
		{
			name: "E of two flags the earlier loses",
			cell: notifCell{settingsArgs: append(append([]string{}, ghosttyFlag...), iterm2Flag...)},
			want: "iterm2",
			why:  "the same rule in the other direction: position decides, so ours cannot be placed 'safely first'",
		},
		{
			name: "F local settings tier is honored",
			cell: notifCell{localChannel: "iterm2"},
			want: "iterm2",
			why:  "settings.local.json is part of the resolution, so absence-checking must read it too",
		},
		{
			// The injected value here is deliberately iterm2, NOT ghostty:
			// auto-detection would produce ghostty from the pinned
			// TERM_PROGRAM, which would make "merged" and "replaced"
			// indistinguishable. iterm2 back = merged per key; ghostty back =
			// the later blob replaced ours wholesale.
			name: "G a later flag replaces the earlier wholesale, not per key",
			cell: notifCell{settingsArgs: append(append([]string{}, iterm2Flag...),
				"--settings", `{"permissions":{"allow":[]}}`)},
			want: "ghostty",
			why:  "THE OTHER LOAD-BEARING ROW. A blob carrying no channel still discarded ours, so --settings flags do not merge. That is why initech stands down entirely when the operator passes their own: appending ours would throw away their permissions and hooks to set one key",
		},
	}
	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			got := runNotifCell(t, c.cell, 75*time.Second)
			t.Logf("CELL %s -> channel=%s after %s", c.name, got.channel, got.elapsed.Round(time.Millisecond))
			if got.channel != c.want {
				t.Fatalf(`MEASURED PRECEDENCE CHANGED: %s

got channel %q, measured %q at Claude Code 2.1.231.
%s

diagnosis: %s`, c.name, got.channel, c.want, c.why, got.payload)
			}
		})
	}
}

// TestNotifChannel_UserSettingsTierIsNotLiveTestable records a LIMIT of this
// rig as a checked fact rather than a silent gap.
//
// Exercising ~/.claude/settings.json without touching the operator's real home
// means redirecting HOME -- and that drops the child into first-run onboarding,
// where it never reaches a dialog, so the cell measures nothing. The control
// (redirected HOME, channel set in the PROJECT tier, which is otherwise known
// to work) is silent too, which is what proves the redirect and not the tier is
// responsible.
//
// If this test ever fails, redirecting HOME has become viable and the user tier
// SHOULD be added to the matrix above. Until then the tier is covered by
// TestNotifChannelArgs_RespectsChannelInUserSettings, which is portable and has
// no such precondition.
func TestNotifChannel_UserSettingsTierIsNotLiveTestable(t *testing.T) {
	requireNotifProbe(t)

	got := runNotifCell(t, notifCell{redirectHome: true, projectChannel: "ghostty"}, 75*time.Second)
	t.Logf("CONTROL redirected home, project channel ghostty -> channel=%s payload=%q", got.channel, got.payload)
	if got.channel == "ghostty" {
		t.Fatal("redirecting HOME no longer breaks the session: the user settings tier is now " +
			"live-testable and belongs in the precedence matrix above")
	}
	if strings.Contains(got.payload, "dialog-reached") {
		t.Fatalf("the control reached a dialog and stayed silent, which is a REAL finding rather "+
			"than a broken precondition -- investigate before trusting the matrix: %s", got.payload)
	}
}

// injectedChannelArgs is the flag the PRODUCT builds, not a copy of it. Taking
// it from notifChannelSettings means the live matrix cannot certify a blob
// initech does not actually ship -- the decomposition gap that lets every part
// pass while the composition is broken.
var injectedChannelArgs = []string{"--settings", notifChannelSettings()}

// TestNotifChannel_ShadowedHostsRescued is the class-ender proving itself: the
// three markers ini-m2e measured as shadowing the pin and deliberately KEPT
// (they carry real non-terminal freight) must go from 90s-silent to emitting,
// with nothing changed but the injected channel.
func TestNotifChannel_ShadowedHostsRescued(t *testing.T) {
	requireNotifProbe(t)

	markers := []struct {
		name string
		env  string
	}{
		{"cursor askpass", "VSCODE_GIT_ASKPASS_MAIN=/Applications/Cursor.app/Contents/Resources/app/extensions/git/dist/askpass-main.js"},
		{"jetbrains bundle id", "__CFBundleIdentifier=com.jetbrains.pycharm"},
		{"visual studio", "VisualStudioVersion=17.0"},
	}
	for _, m := range markers {
		t.Run(m.name, func(t *testing.T) {
			got := runNotifCell(t, notifCell{
				settingsArgs: injectedChannelArgs,
				env:          []string{m.env},
			}, 75*time.Second)
			t.Logf("RESCUE %s -> channel=%s after %s", m.name, got.channel, got.elapsed.Round(time.Millisecond))
			if got.channel != "ghostty" {
				t.Fatalf(`SHADOWED HOST NOT RESCUED: %s still resolves away from the injected channel.

The injection is the whole ini-2fd fix: it is supposed to bypass the terminal
identity resolution these markers hijack. If this is silent, the setting no
longer outranks the marker chain and the class-ender does not end the class --
take it back for a decision rather than re-opening the enumeration game.

marker: %s
observed channel: %s  (%s)`, m.name, m.env, got.channel, got.payload)
			}
		})
	}
}

// TestNotifChannel_ShadowMarkerStillSuppressesWithoutInjection is the negative
// control for the rescue test above. Without it, a build where the markers had
// simply stopped shadowing would show three green rescues and be read as proof
// the injection works, when nothing was being rescued at all.
func TestNotifChannel_ShadowMarkerStillSuppressesWithoutInjection(t *testing.T) {
	requireNotifProbe(t)

	got := runNotifCell(t, notifCell{
		env: []string{"__CFBundleIdentifier=com.jetbrains.pycharm"},
	}, 75*time.Second)
	t.Logf("BASELINE jetbrains marker, no injection -> channel=%s after %s payload=%q",
		got.channel, got.elapsed.Round(time.Millisecond), got.payload)
	if got.channel == "ghostty" {
		t.Fatalf(`THE SHADOW MARKER NO LONGER SUPPRESSES.

This is not a failure of the fix -- it means Claude's resolution changed and the
marker measured in ini-m2e is no longer hijacking the pin. The rescue test above
is now vacuous (it would pass with the injection removed), so re-measure the
shadow set before trusting it.`)
	}
	// Assert the PRECONDITION, not just the silence. A run that never reached
	// a dialog is also silent, and would let this control pass while proving
	// nothing -- which is the only thing that makes the rescue test above mean
	// anything.
	if !strings.Contains(got.payload, "dialog-reached") {
		t.Fatalf(`BASELINE INCONCLUSIVE: the session never reached the permission dialog, so its
silence says nothing about channel resolution and cannot serve as the control
for the rescue test.

diagnosis: %s`, got.payload)
	}
}

// TestNotifChannel_ProductDecisionPathRescuesAShadowedHost is the composed
// run: the flag under test is built by the SHIPPING decision function, on a
// real directory, and then measured end to end against a shadowing marker.
//
// The other tests verify the parts -- NotifChannelArgs decides correctly, the
// argv builder wires it in, the injected blob rescues the host. Each can pass
// while the composition is broken (a decision path that returns nil in
// practice would leave every unit green and every operator silent), so the
// composed path gets its own measurement rather than an inference.
func TestNotifChannel_ProductDecisionPathRescuesAShadowedHost(t *testing.T) {
	requireNotifProbe(t)

	// HOME is deliberately NOT redirected. This cell has to run in the real
	// environment to be a composed measurement at all, and redirecting HOME
	// drops the child into first-run onboarding, where it never reaches a
	// dialog -- measured, and the reason the user-tier cells are inconclusive.
	// The cost is that a developer who has set their own channel legitimately
	// gets no injection here, so that case SKIPS rather than failing: the
	// unit tests own that branch, and a skip states the precondition instead
	// of reporting a suppression as a broken product.
	args := NotifChannelArgs(t.TempDir(), []string{"claude"})
	if len(args) == 0 {
		t.Skip("this machine's own Claude settings specify a notification channel, so the " +
			"decision path correctly declines to inject; the composed path cannot be measured here")
	}

	got := runNotifCell(t, notifCell{
		settingsArgs: args,
		env:          []string{"__CFBundleIdentifier=com.jetbrains.pycharm"},
	}, 75*time.Second)
	t.Logf("COMPOSED product args %q -> channel=%s after %s", args, got.channel, got.elapsed.Round(time.Millisecond))
	if got.channel != "ghostty" {
		t.Fatalf("the composed product path did NOT rescue a shadowed host: channel=%s (%s)",
			got.channel, got.payload)
	}
}
