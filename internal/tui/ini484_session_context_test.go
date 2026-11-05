package tui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// s484Past is an older modification time, so a newer file is the newest.
var s484Past = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)

// ini-484: context_percent and account do not depend on the pane's width.
// The footer stays first per field; what it cannot read comes from the
// pane's transcript (context, model) and cla's active account (user).

// TestMain keeps the package's tests off this machine's cla state: a test
// that wants an account name points claStatePath at a fixture.
func TestMain(m *testing.M) {
	// A child process some tests start inside a pane, never the suite.
	if out := os.Getenv(peerSendHelperEnv); out != "" {
		runPeerSendHelper(out)
		os.Exit(0)
	}
	claStatePath = ""
	restore := isolateMachineCeiling()
	code := m.Run()
	restore()
	os.Exit(code)
}

// s484Reply is a transcript line for one reply: the model and the tokens it
// was sent, split as Claude Code records them.
func s484Reply(model string, input, cacheCreate, cacheRead int64, sidechain bool) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%t,"sessionId":"s484","timestamp":"2026-11-01T05:00:00Z","message":{"model":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":9},"content":[{"type":"text","text":"ok"}]}}`,
		sidechain, model, input, cacheCreate, cacheRead)
}

const (
	s484User    = `{"type":"user","isSidechain":false,"sessionId":"s484","timestamp":"2026-11-01T05:00:00Z","message":{"content":"Reply with the single word ok."}}`
	s484Compact = `{"type":"system","subtype":"compact_boundary","isSidechain":false,"sessionId":"s484","timestamp":"2026-11-01T05:01:00Z","compactMetadata":{"trigger":"manual","preTokens":76915,"postTokens":9000}}`
)

// s484Transcript writes a session file of lines (each closed by a newline)
// into dir and returns its path.
func s484Transcript(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// s484Cla points claStatePath at a state file holding body; "" is no file.
func s484Cla(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := claStatePath
	claStatePath = path
	t.Cleanup(func() { claStatePath = old })
}

// s484Pane is a live pane showing a corpus capture at the capture's width,
// reading its transcript from dir.
func s484Pane(t *testing.T, name, capture, dir string) *Pane {
	t.Helper()
	rows, width := corpusFooter(t, capture)
	p := newEmuPane(name, width, len(rows))
	lvxwPaint(p, rows)
	p.jsonlDir = dir
	return p
}

// s484Read runs one journal poll, as watchJSONL does every half second.
func s484Read(p *Pane) { p.journalTick(&journalTail{}) }

// AC1: Claude Code at 29 columns, the status line led by the branch and cut
// before the percent; the same session at 120 columns. Both report the same
// context, model and account (red at base: the narrow pane is null).
func TestSessionContext_ANarrowPaneReadsWhatAWidePaneReads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix domain socket directly")
	}
	s484Cla(t, `{"active":"Second","lastSwitch":"2026-10-31T00:00:00Z","lastReason":"manual"}`)
	dir := t.TempDir()
	s484Transcript(t, dir, "a.jsonl", s484User, s484Reply("claude-haiku-5-5", 3, 1200, 75712, false))
	narrow := s484Pane(t, "eng7", "haiku316_branch_cut_w29.txt", dir)
	wide := s484Pane(t, "eng1", "haiku316_branch_w120.txt", dir)
	if f := parseFooter(corpusFooter(t, "haiku316_branch_cut_w29.txt")); f.ContextPercent != nil {
		t.Fatalf("fixture: the 29-column footer reads a context (%d); this test is about one that does not", *f.ContextPercent)
	}
	for _, p := range []*Pane{narrow, wide} {
		s484Read(p)
	}
	got := l6omList(t, &TUI{panes: toPaneViews([]*Pane{narrow, wide})}, true)
	for _, name := range []string{"eng7", "eng1"} {
		if c := string(got[name]["context_percent"]); c != "7" {
			t.Errorf("%s context_percent = %q, want 7 (76915 of 1,000,000 tokens, floored)", name, c)
		}
		if a := string(got[name]["account"]); a != `{"user":"Second","model":"Haiku 5.5"}` {
			t.Errorf("%s account = %s, want the wide pane's", name, a)
		}
	}
}

// The model on a wide pane stays the footer's, suffix and all; a narrow pane
// of the same session names the model without it (equal up to the suffix).
func TestSessionContext_TheFooterStaysFirst(t *testing.T) {
	s484Cla(t, `{"active":"Other"}`)
	p := &Pane{name: "super"}
	p.noteSessionUsage([]JournalEntry{{Type: "assistant", HasUsage: true, Model: "claude-opus-5-5", ContextTokens: 461_000}})
	f := Footer{ContextPercent: l6omInt(46), Model: l6omStr("Opus 5.5 (1M context)"), User: l6omStr("Second")}
	ctx, user, model := paneContext(f, p)
	if ctx == nil || *ctx != 46 || model == nil || *model != "Opus 5.5 (1M context)" || user == nil || *user != "Second" {
		t.Errorf("wide pane: %s %s %s, want the footer's 46, Second, Opus 5.5 (1M context)", l6omShow(ctx), l6omShow(user), l6omShow(model))
	}
	ctx, user, model = paneContext(Footer{}, p)
	if ctx == nil || *ctx != 46 || model == nil || *model != "Opus 5.5" || user == nil || *user != "Other" {
		t.Errorf("narrow pane: %s %s %s, want 46, Other, Opus 5.5", l6omShow(ctx), l6omShow(user), l6omShow(model))
	}
}

// pm's guard: a footer and a transcript that disagree by more than a point
// leave the footer's number and log once per pane and model, naming both;
// one point apart is the same reading a reply later, and says nothing.
func TestSessionContext_ADisagreementIsLoggedOnce(t *testing.T) {
	root := t.TempDir()
	cleanup := InitLogger(root, slog.LevelInfo)
	defer cleanup()
	logged := func() string {
		b, _ := os.ReadFile(filepath.Join(root, ".initech", logFileName))
		return string(b)
	}
	p := &Pane{name: "eng5"}
	p.noteSessionUsage([]JournalEntry{{Type: "assistant", HasUsage: true, Model: "claude-sonnet-5-5", ContextTokens: 610_000}})
	if ctx, _, _ := paneContext(Footer{ContextPercent: l6omInt(60)}, p); *ctx != 60 {
		t.Errorf("one point apart: %d, want the footer's 60", *ctx)
	}
	if strings.Contains(logged(), "disagree") {
		t.Errorf("one point apart was logged:\n%s", logged())
	}
	for range 2 {
		if ctx, _, _ := paneContext(Footer{ContextPercent: l6omInt(30)}, p); *ctx != 30 {
			t.Errorf("thirty points apart: %d, want the footer's 30", *ctx)
		}
	}
	out := logged()
	if n := strings.Count(out, "disagree"); n != 1 {
		t.Errorf("logged %d times, want once:\n%s", n, out)
	}
	for _, w := range []string{"pane=eng5", "model=claude-sonnet-5-5", "footer_pct=30", "session_pct=61"} {
		if !strings.Contains(out, w) {
			t.Errorf("the log line lacks %s:\n%s", w, out)
		}
	}
}

// AC2: a session with no reply yet reads null, not zero; a wide footer fills
// it.
func TestSessionContext_AFreshSessionIsNull(t *testing.T) {
	dir := t.TempDir()
	s484Transcript(t, dir, "a.jsonl", s484User)
	p := &Pane{name: "eng2", jsonlDir: dir}
	s484Read(p)
	if ctx, _, model := paneContext(Footer{}, p); ctx != nil || model != nil {
		t.Errorf("no reply yet: context %s, model %s, want null", l6omShow(ctx), l6omShow(model))
	}
	if ctx, _, _ := paneContext(Footer{ContextPercent: l6omInt(0)}, p); ctx == nil || *ctx != 0 {
		t.Errorf("a wide footer's 0%% was not kept: %s", l6omShow(ctx))
	}
}

// What the transcript says, line by line: the last main-chain reply counts;
// a subagent's or Claude Code's own synthetic one does not; a compaction
// clears the reading until the next reply; a model not measured has none;
// past 100 the window cannot be right.
func TestSessionContext_WhichReplyCounts(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"the last reply", []string{s484Reply("claude-opus-5-5", 1, 2, 120_000, false), s484Reply("claude-opus-5-5", 2, 3000, 130_000, false)}, "13 \"Opus 5.5\""},
		{"a subagent's reply", []string{s484Reply("claude-opus-5-5", 1, 2, 120_000, false), s484Reply("claude-haiku-5-5", 1, 0, 900_000, true)}, "12 \"Opus 5.5\""},
		{"a synthetic reply", []string{s484Reply("claude-fable-5-1", 0, 0, 157_855, false), s484Reply(syntheticModel, 0, 0, 0, false)}, "15 \"Fable 5.1\""},
		{"compacted", []string{s484Reply("claude-opus-5-5", 0, 0, 900_000, false), s484Compact}, "null null"},
		{"compacted, then a reply", []string{s484Reply("claude-opus-5-5", 0, 0, 900_000, false), s484Compact, s484Reply("claude-opus-5-5", 3, 9000, 12_000, false)}, "2 \"Opus 5.5\""},
		{"a model not measured", []string{s484Reply("claude-nova-6-0", 0, 0, 100_000, false)}, "null null"},
		{"past the window", []string{s484Reply("claude-sonnet-5-5", 0, 0, 1_010_000, false)}, "null \"Sonnet 5.5\""},
		{"a reply with no usage", []string{s484Reply("claude-fable-5-1", 0, 0, 157_855, false), `{"type":"assistant","isSidechain":false,"message":{"model":"claude-fable-5-1","content":[{"type":"text","text":"x"}]}}`}, "15 \"Fable 5.1\""},
		{"at the floor", []string{s484Reply("claude-sonnet-5-5", 0, 0, 699_999, false)}, "69 \"Sonnet 5.5\""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		s484Transcript(t, dir, "a.jsonl", c.lines...)
		p := &Pane{name: "qa1", jsonlDir: dir}
		s484Read(p)
		ctx, _, model := paneContext(Footer{}, p)
		if got := l6omShow(ctx) + " " + l6omShow(model); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// A new transcript file (/clear, a new session) starts empty, and so does
// one cut shorter than what was read.
func TestSessionContext_ANewFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	old := s484Transcript(t, dir, "a.jsonl", s484Reply("claude-opus-5-5", 0, 0, 500_000, false))
	p := &Pane{name: "eng4", jsonlDir: dir}
	tail := &journalTail{}
	p.journalTick(tail)
	if ctx, _, _ := paneContext(Footer{}, p); ctx == nil || *ctx != 50 {
		t.Fatalf("fixture: %s, want 50", l6omShow(ctx))
	}
	fresh := s484Transcript(t, dir, "b.jsonl", s484User)
	os.Chtimes(old, s484Past, s484Past)
	p.journalTick(tail)
	if ctx, _, _ := paneContext(Footer{}, p); ctx != nil {
		t.Errorf("after /clear the reading is %d, want null until the first reply", *ctx)
	}
	os.WriteFile(fresh, []byte(s484User+"\n"+s484Reply("claude-opus-5-5", 0, 0, 300_000, false)+"\n"+s484User+"\n"), 0o644)
	p.journalTick(tail)
	if ctx, _, _ := paneContext(Footer{}, p); ctx == nil || *ctx != 30 {
		t.Fatalf("fixture: the first reply after /clear reads %s, want 30", l6omShow(ctx))
	}
	os.WriteFile(fresh, []byte(s484User+"\n"), 0o644)
	p.journalTick(tail)
	if ctx, _, _ := paneContext(Footer{}, p); ctx != nil {
		t.Errorf("a file cut shorter keeps %d, want it read again from the start", *ctx)
	}
}

// AC4, feed it nothing: no session folder, no file, an empty file, a
// malformed last line, a last line still being written; cla's file absent,
// empty, malformed, naming no one.
func TestSessionContext_NothingToRead(t *testing.T) {
	good := s484Reply("claude-haiku-5-5", 3, 1200, 75712, false)
	cases := []struct {
		name string
		body *string // nil: no file
		want string
	}{
		{"no session file", nil, "null"},
		{"an empty session file", new(""), "null"},
		{"a malformed last line", new(good + "\n" + `{"type":"assistant","message":{"model":"claude-haiku-5-5","usage":{"input_tokens":` + "\n"), "7"},
		{"a last line still being written", new(good + "\n" + `{"type":"assistant","message":{"model":"claude-haiku-5-5","usage":{"input_tokens":900000`), "7"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if c.body != nil {
			os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte(*c.body), 0o644)
		}
		p := &Pane{name: "qa3", jsonlDir: dir}
		s484Read(p)
		if ctx, _, _ := paneContext(Footer{}, p); l6omShow(ctx) != c.want {
			t.Errorf("%s: %s, want %s", c.name, l6omShow(ctx), c.want)
		}
	}
	p := &Pane{name: "qa3", jsonlDir: filepath.Join(t.TempDir(), "never-made")}
	s484Read(p)
	if ctx, _, _ := paneContext(Footer{}, p); ctx != nil {
		t.Errorf("no session folder: %d, want null", *ctx)
	}
	if ctx, _, _ := paneContext(Footer{}, nil); ctx != nil {
		t.Errorf("no pane: %d, want null", *ctx)
	}

	for _, c := range []struct{ name, body string }{
		{"no cla file", ""},
		{"an empty cla file", " "},
		{"a malformed cla file", `{"active":`},
		{"cla names no account", `{"active":"","lastReason":"x"}`},
		{"a cla file past its bound", `{"active":"Other","pad":"` + strings.Repeat("x", claStateMax) + `"}`},
	} {
		s484Cla(t, c.body)
		if _, user, _ := paneContext(Footer{}, &Pane{name: "qa3"}); user != nil {
			t.Errorf("%s: user %q, want null", c.name, *user)
		}
	}
	s484Cla(t, `{"active":"Other","lastSwitch":"2026-10-31T00:00:00Z"}`)
	if _, user, _ := paneContext(Footer{}, &Pane{name: "qa3"}); user == nil || *user != "Other" {
		t.Errorf("cla's active account: %s, want Other", l6omShow(user))
	}
}
