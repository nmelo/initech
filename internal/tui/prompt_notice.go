package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// A PROMPT THE AGENT CANNOT PASS ALONE is Claude Code's permission prompt
// showing its auto-block countdown: a guard (an rm on a target it cannot
// resolve, a compound command with cd and a write) asks for approval even in
// bypass mode, and blocks the command itself when the countdown runs out.
// super is told once, from the pane rows the maintenance pass already read,
// and answers in the pane or redirects the agent. initech never answers the
// prompt and never types into the agent's pane for this.

// blockingPrompt is what a notice says about one open prompt, read from the
// screen: the question, the reason line above it, and the time left as the
// screen shows it.
type blockingPrompt struct {
	Question string
	Reason   string
	Left     string
}

// promptQuestion is the question row of Claude Code's permission prompt.
const promptQuestion = "Do you want to proceed?"

// promptCountdownRe matches the auto-block countdown, in the rows joined with
// single spaces so a countdown wrapped over several rows reads as one line.
var promptCountdownRe = regexp.MustCompile(`Claude Code will automatically block this request in (\d+:\d\d)\b`)

// promptNoticeGrace is how long a prompt must stay on screen before super is
// told: a prompt answered sooner was already being handled by the agent or
// the operator.
const promptNoticeGrace = 3 * time.Second

// parseBlockingPrompt reads an open blocking prompt from a pane's screen.
// It reads only the rows where a live dialog can be (liveDialogRows): a
// prompt quoted in the transcript above a resting input box is not open. It
// needs both the question row and the countdown there. Anything else -- no countdown, a picker, a question it does not
// know -- is not a blocking prompt, so a shape it does not recognise sends
// nothing rather than half a notice.
func parseBlockingPrompt(rows []string) (blockingPrompt, bool) {
	live := liveDialogRows(rows)
	q := -1
	for i, r := range live {
		if strings.TrimSpace(r) == promptQuestion {
			q = i
		}
	}
	if q < 0 || !promptOptionsBelow(live[q+1:]) {
		return blockingPrompt{}, false
	}
	// The countdown and the reason sit above the question: the reason as a
	// block of rows starting with │, then the ⚠ countdown, which may wrap.
	var reason, countdown []string
	inCountdown := false
	for _, r := range live[:q] {
		t := strings.TrimSpace(r)
		switch {
		case strings.HasPrefix(t, "│"):
			reason = append(reason, strings.TrimSpace(strings.TrimPrefix(t, "│")))
			inCountdown = false
		case strings.HasPrefix(t, "⚠"):
			countdown = []string{strings.TrimSpace(strings.TrimPrefix(t, "⚠"))}
			inCountdown = true
		case t == "":
			inCountdown = false
		case inCountdown:
			countdown = append(countdown, t)
		default:
			// A row that is neither reason nor countdown ends the reason
			// block before it: only the block nearest the question counts.
			reason = nil
		}
	}
	m := promptCountdownRe.FindStringSubmatch(strings.Join(countdown, " "))
	if m == nil {
		return blockingPrompt{}, false
	}
	return blockingPrompt{
		Question: promptQuestion,
		Reason:   cleanScreenText(strings.Join(reason, " ")),
		Left:     m[1],
	}, true
}

// promptOptionsBelow reports whether the two rows right below the question
// are the prompt's own answers, "1. Yes" and "2. No" (the cursor mark ❯ on
// either). A screen that only shows the question and a countdown -- text an
// agent printed -- is not an open prompt.
func promptOptionsBelow(rows []string) bool {
	var opts []string
	for _, r := range rows {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r), "❯"))
		if t == "" {
			continue
		}
		opts = append(opts, t)
		if len(opts) == 2 {
			break
		}
	}
	return len(opts) == 2 && opts[0] == "1. Yes" && opts[1] == "2. No"
}

// screenTextMax caps the reason text a notice carries.
const screenTextMax = 160

// cleanScreenText makes text read from an agent's screen safe to forward: only
// printable characters, whitespace collapsed, at most screenTextMax runes.
// It is still the agent's screen, and the notice quotes it as such.
func cleanScreenText(s string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if !unicode.IsPrint(r) {
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
			n++
		}
		space = false
		if n >= screenTextMax {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// promptNoticeState is one pane's record of its open blocking prompt.
type promptNoticeState struct {
	key       string    // question and reason of the prompt on screen; "" when none
	firstSeen time.Time // when that prompt was first read
	sent      bool      // super has been told about this prompt
	due       *blockingPrompt
}

// observeBlockingPrompt records what this tick's screen shows. A notice is
// made due once per prompt, after it has stayed on screen for the grace; the
// same prompt staying open makes nothing more due, and a prompt that leaves
// and opens again is a new prompt.
func (p *Pane) observeBlockingPrompt(rows []string, now time.Time) {
	bp, ok := parseBlockingPrompt(rows)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := &p.promptNotice
	if !ok {
		*s = promptNoticeState{}
		return
	}
	key := bp.Question + "\x00" + bp.Reason
	if key != s.key {
		*s = promptNoticeState{key: key, firstSeen: now}
	}
	if !s.sent && now.Sub(s.firstSeen) >= promptNoticeGrace {
		s.sent = true
		s.due = &bp
	}
}

// takeDuePromptNotice returns a notice made due and not yet sent, once.
func (p *Pane) takeDuePromptNotice() (blockingPrompt, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.promptNotice.due == nil {
		return blockingPrompt{}, false
	}
	bp := *p.promptNotice.due
	p.promptNotice.due = nil
	return bp, true
}

// promptNoticeText is the line super reads.
func promptNoticeText(agent string, bp blockingPrompt) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is at a prompt it cannot pass alone: %s", agent, bp.Question)
	if bp.Reason != "" {
		fmt.Fprintf(&b, " Its screen gives the reason: %q.", bp.Reason)
	}
	fmt.Fprintf(&b, " Claude Code blocks it in %s. Answer it: initech keystroke %s y (or n), or tell %s to rewrite the command.", bp.Left, agent, agent)
	return b.String()
}

// sendPromptNotices tells super about every prompt made due this tick. super's
// own prompt is not sent: the line would only wait behind that same prompt.
// It runs on the main loop after the maintenance pass; sendInitechLine sends
// on its own goroutine, to the one local pane named super, through the send
// path and its holds, and never into the agent's pane.
func (t *TUI) sendPromptNotices() {
	for _, pv := range t.panes {
		p, ok := pv.(*Pane)
		if !ok {
			continue
		}
		bp, due := p.takeDuePromptNotice()
		if !due || p.name == superPaneName {
			continue
		}
		LogInfo("prompt", "notice to super", "pane", p.name, "left", bp.Left)
		t.sendInitechLine(promptNoticeText(p.name, bp))
	}
}
