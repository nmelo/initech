package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// ini-hoy: Claude Code collapses a message longer than 800 characters into
// a "[Pasted text #N]" placeholder, and a collapsed paste is sent as content, never read as a slash command. So
// `initech compact super <1,300 characters>` reported delivered and nothing
// compacted. The TUI's send action now refuses such a slash command to a
// Claude Code pane before anything is typed, held or queued.

// hoySlash is a slash command of exactly n runes, starting "/compact ".
func hoySlash(n int) string {
	head := "/compact keep: "
	return head + strings.Repeat("k", n-len(head))
}

func hoyLongWords(n int) string {
	return fmt.Sprintf("a slash command of %d characters would paste, not run; Claude takes a command of at most 800", n)
}

// hoyRefused asserts the reply refuses with the words and nothing reached
// the child.
func hoyRefused(t *testing.T, resp IPCResponse, words string, written string) {
	t.Helper()
	want := "not delivered to eng1: " + words
	if resp.OK || resp.Delivery != DeliveryNotDelivered || resp.Error != want || resp.Data != words {
		t.Errorf("reply %+v, want not delivered with %q", resp, want)
	}
	if written != "" {
		t.Errorf("a refused slash command still wrote %d bytes to the child: %.80q", len(written), written)
	}
}

// AC1: a 1,300-character slash command to a Claude pane: refused with its
// length and the limit, nothing written.
func TestSlashPaste_ALongSlashCommandIsRefusedAndNothingTyped(t *testing.T) {
	p, r := s7qSettledPane(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := s7qSend(t, tui, "eng1", hoySlash(1300))
	hoyRefused(t, resp, hoyLongWords(1300), s7qWritten(t, p, r))
}

// AC2: a line break alone does not paste a short command. MEASURED on
// Claude Code 2.1.316: a submitted two-line /compact ran (ini-hoy), so it is
// typed as today; past the limit, its length is what refuses it.
func TestSlashPaste_AShortCommandWithALineBreakIsTyped(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	text := "/compact keep the plan\nand the tests"
	p, r := s7qSettledPane(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	if resp := s7qSend(t, tui, "eng1", text); strings.Contains(resp.Error, "slash command") {
		t.Errorf("refused: %+v", resp)
	}
	if got := s7qWritten(t, p, r); !strings.Contains(got, "and the tests") {
		t.Errorf("not typed: %.80q", got)
	}
	long := hoySlash(900)[:850] + "\n" + hoySlash(900)[850:]
	p2, r2 := s7qSettledPane(t, "eng1")
	tui2 := &TUI{panes: toPaneViews([]*Pane{p2})}
	hoyRefused(t, s7qSend(t, tui2, "eng1", long), hoyLongWords(901), s7qWritten(t, p2, r2))
}

// AC2: the line boundary, MEASURED on Claude Code 2.1.316 (ini-hoy): a short
// command of 3 lines showed as text and ran; 4 lines collapsed to "[Pasted
// text #1 +3 lines]", and 5, 10 and 20 collapsed and did not run. A command
// of more than claudeSlashMaxLines is refused with its line count; 3 is typed.
func TestSlashPaste_MoreThanThreeLinesIsRefused(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	three := "/compact keep line 1\nline 2\nline 3"
	p, r := s7qSettledPane(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	if resp := s7qSend(t, tui, "eng1", three); strings.Contains(resp.Error, "slash command") {
		t.Errorf("3 lines refused: %+v", resp)
	}
	if got := s7qWritten(t, p, r); !strings.Contains(got, "line 3") {
		t.Errorf("3 lines not typed: %.80q", got)
	}
	for _, c := range []struct {
		text  string
		lines int
	}{
		{three + "\nline 4", 4},
		{three + "\nline 4\nline 5", 5},
		{"/compact a\r\nb\r\nc\r\nd", 4},
		{three + "\n", 4},
	} {
		p, r := s7qSettledPane(t, "eng1")
		tui := &TUI{panes: toPaneViews([]*Pane{p})}
		words := fmt.Sprintf("a slash command of %d lines would paste, not run; Claude takes a command of at most 3 lines", c.lines)
		hoyRefused(t, s7qSend(t, tui, "eng1", c.text), words, s7qWritten(t, p, r))
	}
}

// AC2: the boundary. A command of exactly the limit is typed; one more is
// refused. Counted in characters (runes), not bytes.
func TestSlashPaste_TheLimitIsTypedAndOneMoreIsRefused(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	for _, text := range []string{hoySlash(claudeSlashMaxRunes), "/compact " + strings.Repeat("é", claudeSlashMaxRunes-len("/compact "))} {
		p, r := s7qSettledPane(t, "eng1")
		tui := &TUI{panes: toPaneViews([]*Pane{p})}
		resp := s7qSend(t, tui, "eng1", text)
		if resp.Delivery == DeliveryNotDelivered && strings.Contains(resp.Error, "slash command") {
			t.Errorf("a %d-character command was refused: %+v", len([]rune(text)), resp)
		}
		if got := s7qWritten(t, p, r); !strings.Contains(got, text[len(text)-20:]) {
			t.Errorf("a %d-character command was not typed", len([]rune(text)))
		}
	}
	p, r := s7qSettledPane(t, "eng1")
	tui := &TUI{panes: toPaneViews([]*Pane{p})}
	resp := s7qSend(t, tui, "eng1", hoySlash(claudeSlashMaxRunes+1))
	hoyRefused(t, resp, hoyLongWords(claudeSlashMaxRunes+1), s7qWritten(t, p, r))
}

// AC3: positive twins, each typed as today: a short slash command, a bare
// /compact, a long message that is not a command, and a long command to a
// Codex pane.
func TestSlashPaste_WhatDoesNotPasteAsACommandIsTypedAsToday(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	long := strings.Repeat("plain words ", 110)
	for _, c := range []struct {
		name, agentType, text string
	}{
		{"short command", "", "/compact keep the plan"},
		{"bare compact", "", "/compact"},
		{"long plain text", "", long},
		{"long command to codex", config.AgentTypeCodex, hoySlash(1300)},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, r := s7qSettledPane(t, "eng1")
			p.agentType = c.agentType
			tui := &TUI{panes: toPaneViews([]*Pane{p})}
			resp := s7qSend(t, tui, "eng1", c.text)
			if strings.Contains(resp.Error, "slash command") {
				t.Errorf("refused: %+v", resp)
			}
			if got := s7qWritten(t, p, r); !strings.Contains(got, c.text[len(c.text)-8:]) {
				t.Errorf("not typed; the child got %.80q", got)
			}
		})
	}
}

// AC4: feed-it-nothing. A lone "/" and a pane of an unknown agent type are
// typed as today: only a Claude Code pane is judged.
func TestSlashPaste_ALoneSlashAndAnUnknownAgentAreTypedAsToday(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	for _, c := range []struct{ agentType, text string }{{"", "/"}, {"some-new-agent", hoySlash(1300)}} {
		p, r := s7qSettledPane(t, "eng1")
		p.agentType = c.agentType
		tui := &TUI{panes: toPaneViews([]*Pane{p})}
		if resp := s7qSend(t, tui, "eng1", c.text); strings.Contains(resp.Error, "slash command") {
			t.Errorf("agent %q, %d characters: refused %+v", c.agentType, len(c.text), resp)
		}
		if got := s7qWritten(t, p, r); !strings.Contains(got, c.text) {
			t.Errorf("agent %q: not typed", c.agentType)
		}
	}
}

// AC4: the check runs before every hold. A dialog, do-not-disturb or a
// suspended agent would otherwise keep the command and paste it later, the
// same silent no-op: each is refused with the paste words, nothing queued.
func TestSlashPaste_RefusedBeforeTheDialogDoNotDisturbAndSuspendHolds(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(p *Pane)
	}{
		{"dialog open", func(p *Pane) { p.latchDialogOpen("Claude needs your permission") }},
		{"do-not-disturb", func(p *Pane) { p.startDNDFor(time.Now(), 0) }},
		{"suspended", func(p *Pane) { p.mu.Lock(); p.suspended = true; p.mu.Unlock() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, r := s7qSettledPane(t, "eng1")
			c.set(p)
			tui := &TUI{panes: toPaneViews([]*Pane{p})}
			resp := s7qSend(t, tui, "eng1", hoySlash(1300))
			if q := p.QueuedMessageCount(); q != 0 {
				t.Errorf("%d messages queued, want none", q)
			}
			hoyRefused(t, resp, hoyLongWords(1300), s7qWritten(t, p, r))
		})
	}
}

// The other senders reach a pane through sendText (initech serve, a
// forwarded send from another window): the same refusal, nothing written,
// nothing queued, a suspended pane included.
func TestSlashPaste_TheControlStreamPathRefusesToo(t *testing.T) {
	for _, suspended := range []bool{false, true} {
		p, r := s7qSettledPane(t, "eng1")
		p.mu.Lock()
		p.suspended = suspended
		p.mu.Unlock()
		resp := verdictSend(p, hoySlash(1300), true, sendOrigin{From: "super"})
		if resp.OK || resp.Delivery != DeliveryNotDelivered || resp.Error != hoyLongWords(1300) {
			t.Errorf("suspended %v: %+v, want not delivered with the words", suspended, resp)
		}
		if q := p.QueuedMessageCount(); q != 0 {
			t.Errorf("suspended %v: %d queued, want none", suspended, q)
		}
		if got := s7qWritten(t, p, r); got != "" {
			t.Errorf("suspended %v: wrote %d bytes", suspended, len(got))
		}
	}
}

// The operator's own paste from another window is typing, not a send: it is
// not judged.
func TestSlashPaste_TheOperatorsOwnPasteIsNotJudged(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, r := s7qSettledPane(t, "eng1")
	p.SendOperatorText(hoySlash(1300), false)
	if got := s7qWritten(t, p, r); !strings.Contains(got, "kkkkkkkk") {
		t.Errorf("the operator's paste was not typed: %.80q", got)
	}
}

// IsSlashPasteRefusal recognises the refusal in a sender's error, so
// compact can say "not compacted" without matching the words itself.
func TestSlashPaste_TheRefusalIsRecognisable(t *testing.T) {
	for _, w := range []string{hoyLongWords(1300), "not delivered to eng1: " + hoyLongWords(801), "a slash command of 4 lines would paste, not run; Claude takes a command of at most 3 lines"} {
		if !IsSlashPasteRefusal(w) {
			t.Errorf("%q not recognised", w)
		}
	}
	for _, w := range []string{"", "not delivered to eng1: agent eng1 is stopped", "a slash"} {
		if IsSlashPasteRefusal(w) {
			t.Errorf("%q recognised", w)
		}
	}
}
