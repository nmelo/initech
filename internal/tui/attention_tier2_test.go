//go:build !windows

package tui

// attention_tier2_test.go covers the coverage tier (ini-2x8.5).
//
// The codex fixtures below are transcribed VERBATIM from a real codex-cli
// 0.145.0 session driven under a PTY; the capture record is on ini-2x8.5. There
// is deliberately NO codex per-command approval fixture here: that specimen
// could not be provoked (codex cannot execute any command with this account --
// a 400 "model is not supported when using Codex with a ChatGPT account",
// reproduced outside the rig with the operator's own config), and writing a
// pattern for a dialog nobody has seen is the exact failure the capture
// obligation exists to prevent.

import (
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// Real codex 0.145.0 directory-trust gate, transcribed from the capture.
var codexTrustDialog = []string{
	"> You are in /tmp/example",
	"Do you trust the contents of this directory? Working with untrusted contents",
	"comes with higher risk of prompt injection. Trusting the directory allows",
	"project-local config, hooks, and exec policies to load.",
	"› 1. Yes, continue",
	"  2. No, quit",
	"Press enter to continue",
}

// Real codex 0.145.0 hook-review gate, transcribed from the capture.
var codexHookReviewDialog = []string{
	"Hooks need review",
	"3 hooks are new or changed.",
	"Hooks can run outside the sandbox after you trust them.",
	"› 1. Review hooks",
	"  2. Trust all and continue",
	"  3. Continue without trusting (hooks won't run)",
	"Press enter to confirm or esc to go back",
}

func codexPane(name string) *Pane {
	p := dialogPane(name)
	p.agentType = config.AgentTypeCodex
	return p
}

// ── Detection on the real specimens ─────────────────────────────────

func TestHasTier2Dialog_DetectsTheCapturedCodexDialogs(t *testing.T) {
	for name, fixture := range map[string][]string{
		"trust gate":  codexTrustDialog,
		"hook review": codexHookReviewDialog,
	} {
		if !hasTier2Dialog(strings.Join(fixture, "\n")) {
			t.Errorf("%s: real captured codex dialog not detected:\n%s", name, strings.Join(fixture, "\n"))
		}
	}
}

// TestHasTier2Dialog_NegativeControls is the false-positive guard. These are the
// states an agent spends nearly all its time in; if any of them read as a
// blocking dialog, the list would be permanently full and worthless.
func TestHasTier2Dialog_NegativeControls(t *testing.T) {
	cases := map[string]string{
		"claude idle prompt":  "❯ ",
		"claude busy spinner": "✻ Ruminating… (12s · esc to interrupt)",
		"codex prompt":        "› ",
		"ordinary output":     "Ran 1 shell command\n<!doctype html><html lang=\"en\">",
		"empty":               "",
	}
	for name, text := range cases {
		if hasTier2Dialog(text) {
			t.Errorf("%s falsely detected as a blocking dialog: %q", name, text)
		}
	}
}

// ── Tier-2 is list-only, structurally ───────────────────────────────

func TestRefreshTier2WaitingState_RaisesAtTheSilentTierOnly(t *testing.T) {
	p := codexPane("qa4")
	paint(t, p, codexTrustDialog...)

	p.refreshTier2WaitingState()

	waiting, _, preview := p.WaitingInput()
	if !waiting {
		t.Fatal("a real codex trust dialog did not surface in the list")
	}
	if got := p.WaitingTierOf(); got != WaitingTierListOnly {
		t.Errorf("tier = %v, want WaitingTierListOnly -- codex emits no notification signal at all "+
			"(measured: zero OSC 777 and zero OSC 9 across a capture holding two open dialogs), "+
			"so its state is inferred and inference does not get to interrupt the operator", got)
	}
	if preview != tier2PreviewText {
		t.Errorf("preview = %q, want exactly %q -- claiming more than the heuristic knows "+
			"would be a lie in a box whose whole job is being trusted", preview, tier2PreviewText)
	}
}

// TestRefreshTier2WaitingState_NeverChimes is the end-to-end version of the
// tier rule: a codex pane blocked for ten minutes stays silent.
func TestRefreshTier2WaitingState_NeverChimes(t *testing.T) {
	p := codexPane("qa4")
	paint(t, p, codexHookReviewDialog...)
	p.refreshTier2WaitingState()

	tu, c := chimeTUI(p)
	start := time.Now()
	for i := 0; i <= 10; i++ {
		tu.attentionChimes(start.Add(time.Duration(i) * time.Minute))
	}

	if c.n != 0 {
		t.Errorf("a codex pane chimed %d times across 10 minutes; tier-2 is list-only "+
			"and cannot earn chime rights by any amount of pattern work", c.n)
	}
}

func TestRefreshTier2WaitingState_ClearsWhenTheDialogGoes(t *testing.T) {
	p := codexPane("qa4")
	paint(t, p, codexTrustDialog...)
	p.refreshTier2WaitingState()
	if waiting, _, _ := p.WaitingInput(); !waiting {
		t.Fatal("setup: not waiting")
	}

	paint(t, p, "› ", "codex got on with it")
	p.refreshTier2WaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("codex row survived the dialog leaving the screen")
	}
}

func TestRefreshTier2WaitingState_DeadPaneClearsTheRow(t *testing.T) {
	p := codexPane("qa4")
	paint(t, p, codexTrustDialog...)
	p.refreshTier2WaitingState()

	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
	p.refreshTier2WaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("codex row survived the agent's death")
	}
}

// ── Tier-1 panes are excluded ───────────────────────────────────────

// TestTier2Eligible_ExcludesPanesWithATier1Signal keeps the screen out of the
// chime path by the back door: for a Claude pane OSC 777 is authoritative on
// both edges, so the heuristic must not also drive it.
func TestTier2Eligible_ExcludesPanesWithATier1Signal(t *testing.T) {
	claude := dialogPane("super")
	claude.agentType = config.AgentTypeClaudeCode
	if tier2Eligible(claude) {
		t.Error("a Claude pane is tier-2 eligible; the screen must not drive a pane that declares its own state")
	}

	for _, at := range []string{config.AgentTypeCodex, config.AgentTypeOpenCode, config.AgentTypeGeneric} {
		p := dialogPane("x")
		p.agentType = at
		if !tier2Eligible(p) {
			t.Errorf("agent type %q is not tier-2 eligible, but it has no tier-1 signal", at)
		}
	}
}

func TestRefreshTier2WaitingState_DoesNothingToAClaudePane(t *testing.T) {
	p := dialogPane("super")
	p.agentType = config.AgentTypeClaudeCode
	paint(t, p, permissionPromptDialog...)

	p.refreshTier2WaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("tier-2 drove a Claude pane; tier-1 owns both edges there")
	}
}

// ── The raw-bytes trap ──────────────────────────────────────────────

// TestTier2_MustReadRenderedRowsNotRawPTYBytes pins a measured constraint that
// is invisible in ordinary use and would be easy to "optimise" away.
//
// Codex positions each WORD separately, so its dialog phrases are not contiguous
// in the PTY byte stream. Measured against the real capture: "Do you trust",
// "trust the contents", "Press enter to continue" and "Yes, continue" ALL fail a
// raw-byte substring test, while every one of them passes against the emulator's
// rendered rows. A rewrite of tier-2 into a raw-stream scan would silently break
// codex detection while still looking reasonable.
func TestTier2_MustReadRenderedRowsNotRawPTYBytes(t *testing.T) {
	// A word-positioned stream, exactly the shape codex emits: each word placed
	// with its own cursor-position escape rather than written contiguously.
	var raw strings.Builder
	raw.WriteString("\x1b[2J\x1b[H")
	words := []string{"Do", "you", "trust", "the", "contents", "of", "this", "directory?"}
	col := 1
	for _, w := range words {
		raw.WriteString("\x1b[20;" + itoa(col) + "H")
		raw.WriteString(w)
		col += len(w) + 1
	}
	raw.WriteString("\x1b[21;1HPress enter to continue")

	stream := raw.String()

	// The phrase is genuinely absent from the raw bytes...
	if strings.Contains(stream, "Do you trust the contents") {
		t.Fatal("fixture is wrong: the phrase must NOT be contiguous in the raw stream")
	}

	// ...but present once the emulator has laid it out, which is what tier-2 reads.
	p := codexPane("qa4")
	if _, err := p.emu.Write([]byte(stream)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !paneHasTier2Dialog(p) {
		t.Error("tier-2 missed a word-positioned codex dialog -- it must read the emulator's " +
			"rendered rows, never the raw PTY byte stream")
	}
}
