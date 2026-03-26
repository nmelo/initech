package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecentJSONLEntriesBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	lines := []string{
		`{"type":"progress","timestamp":"2026-03-26T10:00:00Z"}`,
		`{"type":"user","timestamp":"2026-03-26T10:00:01Z","message":{"content":"hello"}}`,
		`{"type":"assistant","timestamp":"2026-03-26T10:00:02Z","message":{"content":[{"type":"text","text":"world"}]}}`,
		`{"type":"system","timestamp":"2026-03-26T10:00:03Z"}`,
		`{"type":"last-prompt"}`,
	}
	writeLines(t, path, lines)

	entries, offset := recentJSONLEntries(path, 0)
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5", len(entries))
	}
	if entries[0].Type != "progress" {
		t.Errorf("entry 0 type = %q, want progress", entries[0].Type)
	}
	if entries[1].Type != "user" {
		t.Errorf("entry 1 type = %q, want user", entries[1].Type)
	}
	if entries[2].Type != "assistant" || entries[2].Content != "world" {
		t.Errorf("entry 2 = {type:%q, content:%q}, want assistant/world", entries[2].Type, entries[2].Content)
	}
	if offset <= 0 {
		t.Errorf("offset = %d, want > 0", offset)
	}
}

func TestRecentJSONLEntriesIncremental(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Write initial entries.
	writeLines(t, path, []string{
		`{"type":"user","timestamp":"2026-03-26T10:00:00Z"}`,
		`{"type":"assistant","timestamp":"2026-03-26T10:00:01Z"}`,
	})
	_, offset := recentJSONLEntries(path, 0)

	// Append more entries.
	appendLines(t, path, []string{
		`{"type":"progress","timestamp":"2026-03-26T10:00:02Z"}`,
		`{"type":"last-prompt"}`,
	})

	entries, _ := recentJSONLEntries(path, offset)
	if len(entries) != 2 {
		t.Fatalf("incremental read: got %d entries, want 2", len(entries))
	}
	if entries[0].Type != "progress" {
		t.Errorf("entry 0 type = %q, want progress", entries[0].Type)
	}
	if entries[1].Type != "last-prompt" {
		t.Errorf("entry 1 type = %q, want last-prompt", entries[1].Type)
	}
}

func TestRecentJSONLEntriesToolUse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	writeLines(t, path, []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}`,
	})

	entries, _ := recentJSONLEntries(path, 0)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].ToolName != "Bash" {
		t.Errorf("tool name = %q, want Bash", entries[0].ToolName)
	}
	if entries[0].Content != "ls -la" {
		t.Errorf("content = %q, want ls -la", entries[0].Content)
	}
}

func TestRecentJSONLEntriesSkipMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	writeLines(t, path, []string{
		`{"type":"user"}`,
		`not json at all`,
		`{"type":"assistant"}`,
	})

	entries, _ := recentJSONLEntries(path, 0)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (malformed skipped)", len(entries))
	}
}

func TestRecentJSONLEntriesCRLF(t *testing.T) {
	// ini-a1e.11: CRLF line endings must not cause offset drift that leads to
	// duplicate or skipped entries on subsequent incremental reads.
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Write CRLF-terminated lines.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"type\":\"user\"}\r\n")
	f.WriteString("{\"type\":\"assistant\"}\r\n")
	f.Close()

	_, offset := recentJSONLEntries(path, 0)

	// Append one more CRLF line.
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"type\":\"progress\"}\r\n")
	f.Close()

	entries, _ := recentJSONLEntries(path, offset)
	if len(entries) != 1 {
		t.Fatalf("CRLF incremental read: got %d entries, want 1 (offset drift causes duplicates or misses)", len(entries))
	}
	if entries[0].Type != "progress" {
		t.Errorf("entry type = %q, want progress", entries[0].Type)
	}
}

func TestTruncateContent(t *testing.T) {
	short := "hello"
	if got := truncateContent(short); got != "hello" {
		t.Errorf("short: got %q, want %q", got, "hello")
	}

	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}
	got := truncateContent(string(long))
	if len(got) > maxContentLen+20 {
		t.Errorf("truncated length = %d, want <= %d", len(got), maxContentLen+20)
	}
	if got[len(got)-1] != ']' { // ends with "[truncated]"
		t.Error("truncated content should end with [truncated]")
	}
}

func TestPaneRecentEntries(t *testing.T) {
	p := &Pane{}
	// Push more than ring size.
	for i := 0; i < journalRingSize+5; i++ {
		p.journal = append(p.journal, JournalEntry{Type: "user"})
		if len(p.journal) > journalRingSize {
			p.journal = p.journal[1:]
		}
	}
	entries := p.RecentEntries()
	if len(entries) != journalRingSize {
		t.Errorf("ring buffer size = %d, want %d", len(entries), journalRingSize)
	}
}

// Helpers

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		f.WriteString(l + "\n")
	}
	f.Close()
}

func appendLines(t *testing.T, path string, lines []string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		f.WriteString(l + "\n")
	}
	f.Close()
}
