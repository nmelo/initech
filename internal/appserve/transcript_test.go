package appserve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// tline builds one transcript line.
func tline(typ, uuid string, at time.Time, content any) string {
	c, _ := json.Marshal(content)
	rec := map[string]any{"type": typ, "uuid": uuid, "timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": typ, "content": json.RawMessage(c)}}
	b, _ := json.Marshal(rec)
	return string(b)
}

func writeTranscript(t *testing.T, lines []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sess-1.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var t0 = time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)

// shapesFixture is one turn of each shape, with the records a conversation
// leaves out mixed in.
func shapesFixture() []string {
	return []string{
		`{"type":"mode","sessionId":"s"}`,
		tline("user", "u1", t0, "ship the fix tonight"),
		tline("user", "u2", t0.Add(time.Second), "<system-reminder>ignore me</system-reminder>[from super] ini-x: Fix the thing. Read bd show ini-x."),
		tline("assistant", "a0", t0.Add(2*time.Second), []map[string]any{{"type": "thinking", "thinking": "private"}}),
		tline("assistant", "a1", t0.Add(3*time.Second), []map[string]any{{"type": "tool_use", "id": "tu1", "name": "Bash",
			"input": map[string]any{"command": "make check-fast\necho done", "description": "Run check-fast"}}}),
		tline("user", "r1", t0.Add(4*time.Second), []map[string]any{{"type": "tool_result", "tool_use_id": "tu1", "content": "go build ./...\nok"}}),
		tline("assistant", "a2", t0.Add(5*time.Second), []map[string]any{{"type": "text", "text": "Landed it.\n\nCI is green.\n\n- ▶ Approve the release? Recommend yes.\n▶ Restart hover?"}}),
		`{"type":"user","uuid":"m1","isMeta":true,"message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"api-request","blob":"x"}`,
	}
}

// AC2: the conversation renders the transcript to the message shapes:
// Nelson's text marked as his, another agent's message with its sender,
// a tool run as one line that expands to its input and result, the reply
// as text with its footer lines attached to it; thinking, meta and other
// records left out, and system reminders stripped.
func TestConversation_RendersTheMessageShapes(t *testing.T) {
	pg, err := ReadPage(writeTranscript(t, shapesFixture()), 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !pg.StartReached || pg.Before != 0 || pg.Session != "sess-1" || pg.Skipped != 0 {
		t.Errorf("page: start %v before %d session %q skipped %d", pg.StartReached, pg.Before, pg.Session, pg.Skipped)
	}
	got := pg.Messages
	if len(got) != 4 {
		t.Fatalf("%d messages, want 4: %+v", len(got), got)
	}
	want0 := Message{ID: "u1", At: t0, Kind: KindNelson, Text: "ship the fix tonight"}
	if !reflect.DeepEqual(got[0], want0) {
		t.Errorf("Nelson's message = %+v", got[0])
	}
	if got[1].Kind != KindMessage || got[1].From != "super" || got[1].Text != "ini-x: Fix the thing. Read bd show ini-x." {
		t.Errorf("super's message = %+v", got[1])
	}
	tool := got[2]
	if tool.Kind != KindTool || tool.Tool == nil || tool.Text != "Bash: Run check-fast" || tool.Tool.Summary != tool.Text ||
		!strings.Contains(tool.Tool.Detail, `"command":"make check-fast\necho done"`) || !strings.Contains(tool.Tool.Detail, "go build ./...\nok") {
		t.Errorf("tool run = %+v %+v", tool, tool.Tool)
	}
	reply := got[3]
	if reply.Kind != KindAgent || reply.Text != "Landed it.\n\nCI is green." ||
		!reflect.DeepEqual(reply.Footer, []string{"- ▶ Approve the release? Recommend yes.", "▶ Restart hover?"}) {
		t.Errorf("reply = %q footer %q", reply.Text, reply.Footer)
	}
	for _, m := range got {
		if strings.Contains(m.Text, "private") || strings.Contains(m.Text, "system-reminder") || strings.Contains(m.Text, "/clear") {
			t.Errorf("a left-out record shows: %+v", m)
		}
	}
}

// AC2: paging back with before reaches the transcript start, and the pages
// together are the whole conversation, nothing missing or twice; a tool run
// whose result falls in the newer page still carries it.
func TestConversation_ScrollbackReachesTheStart(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		at := t0.Add(time.Duration(i) * time.Minute)
		lines = append(lines, tline("user", fmt.Sprintf("u%d", i), at, fmt.Sprintf("question %d", i)))
		// Two runs in parallel, so a page can end between the first run and
		// its result, which lies past the second.
		for _, n := range []string{"x", "y"} {
			lines = append(lines, tline("assistant", fmt.Sprintf("t%s%d", n, i), at, []map[string]any{{"type": "tool_use", "id": fmt.Sprintf("tu%s%d", n, i), "name": "Read",
				"input": map[string]any{"file_path": fmt.Sprintf("/f%s%d", n, i)}}}))
		}
		for _, n := range []string{"x", "y"} {
			lines = append(lines, tline("user", fmt.Sprintf("r%s%d", n, i), at, []map[string]any{{"type": "tool_result", "tool_use_id": fmt.Sprintf("tu%s%d", n, i), "content": fmt.Sprintf("contents %d", i)}}))
		}
		lines = append(lines, tline("assistant", fmt.Sprintf("a%d", i), at, []map[string]any{{"type": "text", "text": fmt.Sprintf("answer %d", i)}}))
	}
	path := writeTranscript(t, lines)
	whole, err := ReadPage(path, 0, 1000)
	if err != nil || len(whole.Messages) != 160 || !whole.StartReached {
		t.Fatalf("whole: %d messages, start %v, %v", len(whole.Messages), whole.StartReached, err)
	}

	var pages [][]Message
	before := int64(0)
	for n := 0; ; n++ {
		if n > 100 {
			t.Fatal("paging never reached the start")
		}
		pg, err := ReadPage(path, before, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(pg.Messages) == 0 && !pg.StartReached {
			t.Fatal("an empty page before the start")
		}
		pages = append([][]Message{pg.Messages}, pages...)
		if pg.StartReached {
			break
		}
		if pg.Before <= 0 || (before != 0 && pg.Before >= before) {
			t.Fatalf("cursor did not move back: %d after %d", pg.Before, before)
		}
		before = pg.Before
	}
	var joined []Message
	for _, p := range pages {
		joined = append(joined, p...)
	}
	if !reflect.DeepEqual(joined, whole.Messages) {
		t.Errorf("pages joined differ from the whole: %d vs %d messages", len(joined), len(whole.Messages))
		for i := range joined {
			if i < len(whole.Messages) && !reflect.DeepEqual(joined[i], whole.Messages[i]) {
				t.Fatalf("first difference at %d:\n%+v\n%+v", i, joined[i], whole.Messages[i])
			}
		}
	}
	for _, m := range joined {
		if m.Kind == KindTool && !strings.Contains(m.Tool.Detail, "contents ") {
			t.Errorf("tool run %s has no result: %q", m.ID, m.Tool.Detail)
		}
	}
}

// AC6 feed-it-nothing: an empty transcript is no messages and the start; a
// malformed line is left out and counted while the rest renders; a last
// line still being written (no newline) is not read yet; no file is an
// error.
func TestConversation_EmptyMalformedAndHalfWritten(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "e.jsonl")
	os.WriteFile(empty, nil, 0o600)
	pg, err := ReadPage(empty, 0, 50)
	if err != nil || len(pg.Messages) != 0 || !pg.StartReached || pg.Messages == nil {
		t.Errorf("empty: %+v %v", pg, err)
	}

	lines := []string{tline("user", "u1", t0, "first"), `{"type":"user","uuid":`, tline("assistant", "a1", t0, []map[string]any{{"type": "text", "text": "second"}})}
	pg, err = ReadPage(writeTranscript(t, lines), 0, 50)
	if err != nil || len(pg.Messages) != 2 || pg.Skipped != 1 || pg.Messages[1].Text != "second" {
		t.Errorf("malformed line: %d messages, %d skipped, %v", len(pg.Messages), pg.Skipped, err)
	}

	p := filepath.Join(t.TempDir(), "h.jsonl")
	os.WriteFile(p, []byte(tline("user", "u1", t0, "done")+"\n"+`{"type":"assistant","uuid":"a1","mess`), 0o600)
	pg, err = ReadPage(p, 0, 50)
	if err != nil || len(pg.Messages) != 1 || pg.Skipped != 0 {
		t.Errorf("half-written last line: %d messages, %d skipped, %v", len(pg.Messages), pg.Skipped, err)
	}

	if _, err := ReadPage(filepath.Join(t.TempDir(), "none.jsonl"), 0, 50); err == nil {
		t.Error("no transcript file: no error")
	}
}

// AC2, AC6: GET /v1/conversation reads the pane's transcript the TUI names;
// a pane with no transcript is an empty page at the start; an unknown pane
// or rig is 404; a bad limit or cursor is 400; behind the token.
func TestConversationEndpoint_ReadsThePanesTranscript(t *testing.T) {
	path := writeTranscript(t, shapesFixture())
	b := &fakeBackend{rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}},
		panes: map[string][]PaneState{"/r/alpha": {{Name: "super", State: "idle", SessionFile: path}, {Name: "eng1", State: "idle"}}}}
	now := time.Unix(1_800_000_000, 0)
	h := newTestServer(b, &now).Handler()
	auth := "Bearer " + testToken

	code, body := get(t, h, "/v1/conversation/alpha/super?limit=50", auth)
	var pg Page
	if code != 200 || json.Unmarshal([]byte(body), &pg) != nil || len(pg.Messages) != 4 || !pg.StartReached || pg.Session != "sess-1" {
		t.Fatalf("super's conversation: %d %s", code, body)
	}
	code, body = get(t, h, "/v1/conversation/alpha/eng1", auth)
	if code != 200 || !strings.Contains(body, `"messages":[]`) || !strings.Contains(body, `"start_reached":true`) {
		t.Errorf("a pane with no transcript: %d %s", code, body)
	}
	for path, want := range map[string]int{
		"/v1/conversation/alpha/nobody":          404,
		"/v1/conversation/beta/super":            404,
		"/v1/conversation/alpha/super?limit=0":   400,
		"/v1/conversation/alpha/super?limit=x":   400,
		"/v1/conversation/alpha/super?before=-1": 400,
	} {
		if code, _ := get(t, h, path, auth); code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}
	if code, body := get(t, h, "/v1/conversation/alpha/super", ""); code != 401 || body != "" {
		t.Errorf("no token: %d %q", code, body)
	}
}
