package tui

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// Context and account for status --json when the footer cannot say (ini-484).
//
// Claude Code cuts the operator's status line below about 40 columns, and a
// dialog hides it, so in a grid of narrow panes the footer reads nothing.
// The footer stays first for each field it reads; what it could not read
// comes from sources that do not depend on the pane's width:
//
//   - context_percent: the last main-chain reply in the pane's transcript,
//     (input + cache creation + cache read tokens) * 100 / the model's window,
//     floored, which is how the status line computes it;
//   - account.model: that reply's model id, by name;
//   - account.user: cla's active profile (claStateFile), the name the status
//     line prints for every pane on the host.
//
// A model not in sessionModels gives no session number and no name: the
// field stays null rather than a guessed window.

// sessionModel is what a measured model id means: the name the status line
// shows for it and its context window in tokens.
type sessionModel struct {
	name   string
	window int64
}

// sessionModels holds only measured ids. MEASURED 2026-11-01 on Claude Code
// 2.1.316: every live pane of this rig (Opus 5.5, Sonnet 5.5, Fable 5.1) and
// a Haiku 5.5 rig, footer percent against the transcript's last reply, all at
// a 1,000,000-token window (Haiku: 76945 tokens read 7%).
var sessionModels = map[string]sessionModel{
	"claude-opus-5-5":   {"Opus 5.5", 1_000_000},
	"claude-sonnet-5-5": {"Sonnet 5.5", 1_000_000},
	"claude-fable-5-1":  {"Fable 5.1", 1_000_000},
	"claude-haiku-5-5":  {"Haiku 5.5", 1_000_000},
}

// syntheticModel is the model Claude Code writes on replies it makes itself
// (an API error, an interrupt); they carry no usage and say nothing about
// the context.
const syntheticModel = "<synthetic>"

// sessionUsage is the context reading from a pane's transcript: the last
// main-chain reply's model and the tokens it was sent. ok is false before
// the first reply of the file, and after a compaction until the next one.
type sessionUsage struct {
	model  string
	tokens int64
	ok     bool
}

// observe folds journal entries into the reading, in order.
func (u sessionUsage) observe(entries []JournalEntry) sessionUsage {
	for _, e := range entries {
		switch {
		case e.Type == "system" && e.Subtype == "compact_boundary" && !e.Sidechain:
			u = sessionUsage{}
		case e.Type == "assistant" && e.HasUsage && !e.Sidechain && e.Model != syntheticModel:
			u = sessionUsage{model: e.Model, tokens: e.ContextTokens, ok: true}
		}
	}
	return u
}

// percent is the context used, as the status line prints it; nil before a
// reply, for a model not measured, or past 100 (a window that cannot be
// right).
func (u sessionUsage) percent() *int {
	m, known := sessionModels[u.model]
	if !u.ok || !known || u.tokens < 0 {
		return nil
	}
	n := int(u.tokens * 100 / m.window)
	if n > 100 {
		return nil
	}
	return &n
}

// modelName is the reply's model as the status line names it; nil before a
// reply or for a model not measured.
func (u sessionUsage) modelName() *string {
	m, known := sessionModels[u.model]
	if !u.ok || !known {
		return nil
	}
	name := m.name
	return &name
}

// noteSessionUsage folds new transcript entries into the pane's reading.
func (p *Pane) noteSessionUsage(entries []JournalEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessUse = p.sessUse.observe(entries)
}

// resetSessionUsage forgets the reading: a new transcript file starts empty.
func (p *Pane) resetSessionUsage() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessUse = sessionUsage{}
}

// sessionUsageNow returns the pane's transcript reading.
func (p *Pane) sessionUsageNow() sessionUsage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessUse
}

// claStateFile is cla's record of the active account, under the home
// folder. Only its "active" field is read, and its value is never logged.
const claStateFile = ".claude-accounts/state.json"

// claStatePath is where claActiveProfile reads; "" reads nothing (no such
// file). Tests point it at a fixture.
var claStatePath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, claStateFile)
}()

// claStateMax bounds the read: the file holds a name and two short fields.
const claStateMax = 64 << 10

// claActiveProfile is the account name cla's status line prints; nil when
// the file is absent, unreadable, malformed or names no account.
func claActiveProfile() *string {
	f, err := os.Open(claStatePath)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, claStateMax))
	if err != nil {
		return nil
	}
	var st struct {
		Active string `json:"active"`
	}
	if json.Unmarshal(b, &st) != nil || st.Active == "" {
		return nil
	}
	return &st.Active
}

// paneContext merges a pane's footer reading with its other sources, per
// field: the footer first, then the transcript (context, model) or cla's
// active account (user). When both the footer and the transcript give a
// context and they differ by more than one point, the footer's stands and
// the pane logs it once per model: a wrong window in sessionModels shows
// here instead of on every narrow pane unsaid.
func paneContext(f Footer, p *Pane) (ctx *int, user, model *string) {
	ctx, user, model = f.ContextPercent, f.User, f.Model
	var u sessionUsage
	if p != nil {
		u = p.sessionUsageNow()
	}
	sp := u.percent()
	switch {
	case ctx == nil:
		ctx = sp
	case sp != nil && (*ctx-*sp > 1 || *sp-*ctx > 1):
		p.warnContextDisagrees(u.model, *ctx, *sp)
	}
	if model == nil {
		model = u.modelName()
	}
	if user == nil {
		user = claActiveProfile()
	}
	return ctx, user, model
}

// warnContextDisagrees logs, once per model, that the footer's context and
// the transcript's differ.
func (p *Pane) warnContextDisagrees(model string, footer, session int) {
	p.mu.Lock()
	if p.ctxWarned == nil {
		p.ctxWarned = map[string]bool{}
	}
	seen := p.ctxWarned[model]
	p.ctxWarned[model] = true
	p.mu.Unlock()
	if !seen {
		LogWarn("status", "footer and transcript disagree on context; the footer's stands",
			"pane", p.name, "model", model, "footer_pct", footer, "session_pct", session)
	}
}
