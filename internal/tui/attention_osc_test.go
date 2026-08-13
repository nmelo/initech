package tui

// attention_osc_test.go pins OUR PARSING of the OSC 777 notification against the
// bytes Claude Code actually emits (ini-2x8.2).
//
// THIS FILE IS DELIBERATELY UNTAGGED, and that is the point of ini-47w. Its only
// dependency is a const string and an emulator, both portable, so it runs on
// every platform CI covers -- including Windows, where a detector that silently
// stopped recognising the sequence would otherwise never be exercised at all.
//
// Its sibling attention_emitter_probe_test.go holds the LIVE probe, which spawns
// a real Claude Code under a PTY and is correctly '//go:build !windows'. Keeping
// the portable half here is what stops that one legitimate constraint from
// propagating: measuredOSC777 previously lived in the tagged file, so every file
// consuming it had to be tagged too, and two whole suites fell off Windows CI
// without anyone choosing that.
//
// Fixture provenance: captured 2026-08-12 from Claude Code 2.1.229 on macOS via
// a PTY, project-local settings forcing an approval prompt. Full capture record
// is on ini-2x8.

import "testing"

// measuredOSC777 is the exact sequence Claude Code 2.1.229 emits when a blocking
// dialog opens. Byte-for-byte from the capture; do not "tidy" it.
const measuredOSC777 = "\x1b]777;notify;Claude Code;Claude needs your permission\x07"

// measuredOSC777Idle is the emission that caused ini-zfm: the SAME OSC command,
// sent with NO dialog open, when a turn has been idle past
// messageIdleNotifThresholdMs (60s by default).
//
// Captured 2026-08-13 from Claude Code 2.1.229 under a PTY: a prompt needing no
// tools, run to completion, then left alone. One emission, at t+64s, byte-for-
// byte as below. It is the negative control for every dialog fixture in this
// file -- the discriminator between "a human must act" and "Claude is chatty"
// is the message text, and nothing else on the wire carries it.
const measuredOSC777Idle = "\x1b]777;notify;Claude Code;Claude is waiting for your input\x07"

// TestAttentionOSC_FixtureMatchesTheMeasuredEmission feeds the captured bytes
// through a real emulator with the real handler attached, and asserts the pane
// ends up waiting at chime grade.
func TestAttentionOSC_FixtureMatchesTheMeasuredEmission(t *testing.T) {
	p := testPane("super")
	p.attn = &attentionSignal{}
	registerAttentionOSC(p)

	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("emulator write: %v", err)
	}

	p.refreshWaitingState()

	waiting, _, _ := p.WaitingInput()
	if !waiting {
		t.Fatal("the measured OSC 777 emission did not raise WaitingInput -- " +
			"tier-1 detection no longer recognises the sequence Claude Code actually sends")
	}
	if got := p.WaitingTierOf(); got != WaitingTierChime {
		t.Errorf("tier = %v, want WaitingTierChime (the app declaring its own state is chime-grade)", got)
	}
}

// TestAttentionOSC_IgnoresOtherOSCTraffic is the false-positive control. The
// same stream carries window titles and progress reports constantly; if any of
// those raised the state, the chime would fire on ordinary work and a false
// chime is a defect.
func TestAttentionOSC_IgnoresOtherOSCTraffic(t *testing.T) {
	// All measured from the same captures as the notify sequence above.
	noise := []string{
		"\x1b]0;✳ Claude Code\x07",                      // window title
		"\x1b]0;◐ Run Unix timestamp shell command\x07", // title with spinner glyph
		"\x1b]9;4;3;\x07",                               // progress: busy
		"\x1b]9;4;0;\x07",                               // progress: done
		"\x1b]8;id=zaxmda;https://example.com\x07",      // hyperlink
	}
	for _, seq := range noise {
		p := testPane("eng1")
		p.attn = &attentionSignal{}
		registerAttentionOSC(p)

		if _, err := p.emu.Write([]byte(seq)); err != nil {
			t.Fatalf("emulator write: %v", err)
		}
		p.refreshWaitingState()

		if waiting, _, _ := p.WaitingInput(); waiting {
			t.Errorf("ordinary OSC traffic raised WaitingInput: %q", seq)
		}
	}
}

// TestAttentionOSC_IdleNotificationNeverRaises is the negative control this
// feature was missing, and the reason ini-zfm happened (measured 2026-08-13,
// Claude Code 2.1.229).
//
// OSC 777 is not the dialog signal the detector originally assumed. It is
// Claude's general notification channel: a turn that ends with NO dialog open
// emits measuredOSC777Idle at t+64s, on a 60s default threshold. Raising on it
// gave every agent a needs-input row at turn-end -- empty preview, chime-grade,
// and unclearable, because a row whose dialog was never on screen can never earn
// its clear. The operator saw one tick to 4m39s next to an idle prompt.
//
// If this test ever goes green-to-red, tier-1 has gone back to raising on any
// notification and the phantom row is back.
func TestAttentionOSC_IdleNotificationNeverRaises(t *testing.T) {
	p := testPane("super")
	p.attn = &attentionSignal{}
	registerAttentionOSC(p)

	if _, err := p.emu.Write([]byte(measuredOSC777Idle)); err != nil {
		t.Fatalf("emulator write: %v", err)
	}
	p.refreshWaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Fatal("the MEASURED idle notification raised a needs-input row. No dialog is open " +
			"when Claude sends this, so the row has an empty preview and can never clear -- " +
			"and it is chime-grade, so it also rings the bell for nothing")
	}
}

// TestAttentionOSC_UnknownNotificationsNeverRaise pins the ALLOWLIST shape
// rather than the single text that caused the bug. Claude has ten notification
// types and they all ride this one OSC command; a blocklist of today's idle
// message would re-break on the next one they add.
//
// The texts below are the other dispatches found in the 2.1.229 binary. They are
// NOT captures -- they are here to prove the DEFAULT is exclusion, which is the
// property that survives a Claude upgrade.
func TestAttentionOSC_UnknownNotificationsNeverRaise(t *testing.T) {
	others := []string{
		"Claude is waiting for your input",   // idle_prompt (measured)
		"Claude Code login successful",       // auth_success
		"Claude is done using your computer", // computer_use_exit
		"eng1 needs permission for Bash",     // worker_permission_prompt (subagent, not this pane)
		"",                                   // malformed payload with no message field
		"Some notification type invented next quarter",
	}
	for _, msg := range others {
		p := testPane("eng1")
		p.attn = &attentionSignal{}
		registerAttentionOSC(p)

		if _, err := p.emu.Write([]byte("\x1b]777;notify;Claude Code;" + msg + "\x07")); err != nil {
			t.Fatalf("emulator write: %v", err)
		}
		p.refreshWaitingState()

		if waiting, _, _ := p.WaitingInput(); waiting {
			t.Errorf("notification %q raised a needs-input row; only the measured dialog "+
				"texts may raise, so that a new Claude notification type can cost a missed "+
				"raise but never a false one", msg)
		}
	}
}

func TestNotifyMessage_ParsesTheMeasuredPayload(t *testing.T) {
	cases := map[string]string{
		"notify;Claude Code;Claude needs your permission":     "Claude needs your permission",
		"notify;Claude Code;Claude is waiting for your input": "Claude is waiting for your input",
		"notify;Claude Code": "",
		"notify;":            "",
	}
	for in, want := range cases {
		if got := notifyMessage(in); got != want {
			t.Errorf("notifyMessage(%q) = %q, want %q", in, got, want)
		}
	}
}
