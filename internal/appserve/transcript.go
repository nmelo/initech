package appserve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// Message kinds in a conversation.
const (
	KindAgent   = "agent"   // the agent's reply text
	KindNelson  = "nelson"  // what the operator typed into the pane
	KindMessage = "message" // a message another agent sent ("[from <agent>] ...")
	KindTool    = "tool"    // one tool run: a line that expands
)

// Limits on reading a transcript page.
const (
	ToolDetailMax   = 4 << 10  // a tool run's expanded detail, input and result together
	pageReadMax     = 8 << 20  // bytes read backward for one page
	resultLookAhead = 1 << 20  // bytes read past a page to find its last tool runs' results
	readChunk       = 64 << 10 // the backward read's step
)

// Message is one entry of a pane's conversation, rendered from its Claude
// Code session transcript.
type Message struct {
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	From   string    `json:"from,omitempty"`   // KindMessage: the sending agent
	Text   string    `json:"text"`             // the reply or message text; a tool run's one line
	Footer []string  `json:"footer,omitempty"` // KindAgent: the footer lines (▶) at the reply's end
	Tool   *ToolRun  `json:"tool,omitempty"`   // KindTool
}

// ToolRun is one tool run: its name, the one line its collapsed row shows,
// and the input and result it expands to (Detail, at most ToolDetailMax).
type ToolRun struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// Page is one page of a conversation, oldest first. Before is the cursor
// for the next older page (the byte offset of this page's first line);
// StartReached is true when this page begins at the transcript's start.
// Skipped counts the malformed lines left out of this page.
type Page struct {
	Session      string    `json:"session"`
	Messages     []Message `json:"messages"`
	StartReached bool      `json:"start_reached"`
	Before       int64     `json:"before"`
	Skipped      int       `json:"skipped_lines"`
}

// record is the part of a transcript line a conversation reads.
type record struct {
	Type      string    `json:"type"`
	UUID      string    `json:"uuid"`
	Timestamp time.Time `json:"timestamp"`
	IsMeta    bool      `json:"isMeta"`
	Message   *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

var (
	fromPrefix     = regexp.MustCompile(`^\[from ([A-Za-z0-9_.:-]+)\]\s*`)
	systemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
)

// ReadPage is the page of the conversation in the transcript at path that
// ends just before byte offset before (0, or past the end: the newest
// page): whole lines read backward until limit of them carry a message or
// the start is reached, rendered oldest first. A tool run whose result lies
// after the page gets it from a bounded read forward.
func ReadPage(path string, before int64, limit int) (Page, error) {
	pg := Page{Messages: []Message{}}
	f, err := os.Open(path)
	if err != nil {
		return pg, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return pg, err
	}
	pg.Session = strings.TrimSuffix(st.Name(), ".jsonl")
	end := st.Size()
	if before > 0 && before < end {
		end = before
	}
	if limit <= 0 {
		limit = 50
	}

	// Collect whole lines backward from end, newest first. start is the
	// offset of the oldest line taken; tail holds the bytes before it not
	// yet split into lines.
	var lines [][]byte
	pos, start, read, bearing := end, end, int64(0), 0
	var tail []byte
	first := true
	for pos > 0 && bearing < limit && read < pageReadMax {
		n := min(int64(readChunk), pos)
		chunk := make([]byte, n)
		if _, err := f.ReadAt(chunk, pos-n); err != nil && err != io.EOF {
			return pg, err
		}
		pos -= n
		read += n
		tail = append(chunk, tail...)
		if first {
			first = false
			// A last line with no newline yet is still being written.
			if end == st.Size() {
				if i := bytes.LastIndexByte(tail, '\n'); i >= 0 {
					start = pos + int64(i) + 1
					tail = tail[:i+1]
				} else if pos > 0 {
					start = pos
					tail = tail[:0]
				}
			}
		}
		for bearing < limit {
			i := bytes.LastIndexByte(tail, '\n')
			if i < 0 {
				break
			}
			if ln := bytes.TrimSpace(tail[i+1:]); len(ln) > 0 {
				lines = append(lines, ln)
				if bearsMessage(ln) {
					bearing++
				}
			}
			start = pos + int64(i) + 1
			tail = tail[:i]
		}
	}
	if pos == 0 && bearing < limit && read < pageReadMax {
		if ln := bytes.TrimSpace(tail); len(ln) > 0 {
			lines = append(lines, ln)
		}
		start = 0
	}
	pg.Before = start
	pg.StartReached = start == 0

	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	var r renderer
	for _, ln := range lines {
		r.line(ln)
	}
	if r.open() && end < st.Size() {
		r.lookAhead(io.NewSectionReader(f, end, min(st.Size()-end, resultLookAhead)))
	}
	pg.Messages = append(pg.Messages, r.msgs...)
	pg.Skipped = r.skipped
	return pg, nil
}

// bearsMessage reports a line that renders to at least one message, cheaply:
// a user or assistant record with text or a tool use. A malformed line
// bears none.
func bearsMessage(ln []byte) bool {
	var r renderer
	r.line(ln)
	return len(r.msgs) > 0
}

// renderer turns transcript lines, oldest first, into messages, pairing
// each tool run with its result.
type renderer struct {
	msgs    []Message
	tools   map[string]int // tool_use id -> index in msgs, while its result is due
	skipped int
}

func (r *renderer) open() bool { return len(r.tools) > 0 }

// line renders one transcript line.
func (r *renderer) line(ln []byte) {
	var rec record
	if err := json.Unmarshal(ln, &rec); err != nil {
		r.skipped++
		return
	}
	if rec.Message == nil || rec.IsMeta || (rec.Type != "user" && rec.Type != "assistant") {
		return
	}
	var blocks []block
	if len(rec.Message.Content) > 0 && rec.Message.Content[0] == '"' {
		var s string
		if json.Unmarshal(rec.Message.Content, &s) != nil {
			r.skipped++
			return
		}
		blocks = []block{{Type: "text", Text: s}}
	} else if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		r.skipped++
		return
	}
	for i, b := range blocks {
		id := rec.UUID
		if i > 0 {
			id += "/" + itoaInt(i)
		}
		switch {
		case b.Type == "text" && rec.Type == "assistant":
			text := strings.TrimSpace(b.Text)
			if text == "" {
				continue
			}
			body, footer := splitFooter(text)
			r.msgs = append(r.msgs, Message{ID: id, At: rec.Timestamp, Kind: KindAgent, Text: body, Footer: footer})
		case b.Type == "text" && rec.Type == "user":
			text := strings.TrimSpace(systemReminder.ReplaceAllString(b.Text, ""))
			if text == "" {
				continue
			}
			m := Message{ID: id, At: rec.Timestamp, Kind: KindNelson, Text: text}
			if sm := fromPrefix.FindStringSubmatch(text); sm != nil {
				m.Kind, m.From, m.Text = KindMessage, sm[1], strings.TrimSpace(text[len(sm[0]):])
			}
			r.msgs = append(r.msgs, m)
		case b.Type == "tool_use":
			input := compactJSON(b.Input)
			r.msgs = append(r.msgs, Message{ID: id, At: rec.Timestamp, Kind: KindTool, Text: toolSummary(b.Name, b.Input),
				Tool: &ToolRun{Name: b.Name, Summary: toolSummary(b.Name, b.Input), Detail: capText(input, ToolDetailMax)}})
			if r.tools == nil {
				r.tools = map[string]int{}
			}
			r.tools[b.ID] = len(r.msgs) - 1
		case b.Type == "tool_result":
			r.result(b)
		}
	}
}

// result attaches a tool result to its run, when the run is in this page.
func (r *renderer) result(b block) {
	i, ok := r.tools[b.ToolUseID]
	if !ok {
		return
	}
	delete(r.tools, b.ToolUseID)
	t := r.msgs[i].Tool
	t.Detail = capText(t.Detail+"\n\n"+resultText(b.Content), ToolDetailMax)
}

// lookAhead reads lines after the page for the results of its open runs.
func (r *renderer) lookAhead(rd io.Reader) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64<<10), resultLookAhead)
	for sc.Scan() && r.open() {
		var rec record
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "user" || rec.Message == nil {
			continue
		}
		var blocks []block
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				r.result(b)
			}
		}
	}
}

// splitFooter is a reply split at its footer: the trailing lines that
// start with "▶" (or "- ▶"), the super's items for Nelson.
func splitFooter(text string) (body string, footer []string) {
	lines := strings.Split(text, "\n")
	i := len(lines)
	for i > 0 {
		t := strings.TrimSpace(lines[i-1])
		if t == "" && i < len(lines) {
			i--
			continue
		}
		if strings.HasPrefix(t, "▶") || strings.HasPrefix(t, "- ▶") {
			i--
			continue
		}
		break
	}
	for _, l := range lines[i:] {
		if t := strings.TrimSpace(l); t != "" {
			footer = append(footer, t)
		}
	}
	return strings.TrimSpace(strings.Join(lines[:i], "\n")), footer
}

// toolSummary is a tool run's one line: the tool and the input that names
// what it did (a Bash run's description or command, a file's path, a
// search's pattern), cut to 120 characters.
func toolSummary(name string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		return ""
	}
	what := pick("description", "command", "file_path", "path", "pattern", "query", "url", "prompt", "skill")
	what, _, _ = strings.Cut(strings.TrimSpace(what), "\n")
	line := name
	if what != "" {
		line += ": " + what
	}
	return capText(line, 120)
}

// resultText is a tool result's text: a string, or its text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []block
	if json.Unmarshal(raw, &bs) == nil {
		var parts []string
		for _, b := range bs {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// capText cuts s to at most n bytes on a rune boundary, marking the cut.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func itoaInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
