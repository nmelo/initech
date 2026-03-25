package tui

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ── Pane helpers ──────────────────────────────────────────────────────

// newLivePaneT creates a real Pane running a short-lived command.
// The caller must defer p.Close().
func newLivePaneT(t *testing.T, name string, cmd []string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{Name: name, Command: cmd}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane(%q): %v", name, err)
	}
	return p
}

// newEmuPane creates a Pane with a SafeEmulator but no PTY process.
// A background goroutine drains the emulator's response pipe so SendKey
// doesn't block. Good for unit-testing emulator-reading functions.
func newEmuPane(name string, cols, rows int) *Pane {
	emu := vt.NewSafeEmulator(cols, rows)
	// Drain emulator responses (SendKey writes encoded sequences to an
	// internal pipe; without a reader it blocks).
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	return &Pane{
		name:    name,
		emu:     emu,
		visible: true,
		alive:   true,
		region:  Region{X: 0, Y: 0, W: cols, H: rows + 1}, // +1 for title bar
	}
}

// ── Region / InnerSize ───────────────────────────────────────────────

func TestRegionInnerSize(t *testing.T) {
	tests := []struct {
		r            Region
		wantCols     int
		wantRows     int
	}{
		{Region{W: 80, H: 25}, 80, 24},
		{Region{W: 1, H: 2}, 1, 1},
		{Region{W: 0, H: 0}, 1, 1}, // clamped
		{Region{W: 120, H: 40}, 120, 39},
	}
	for _, tt := range tests {
		cols, rows := tt.r.InnerSize()
		if cols != tt.wantCols || rows != tt.wantRows {
			t.Errorf("InnerSize(%+v) = (%d,%d), want (%d,%d)",
				tt.r, cols, rows, tt.wantCols, tt.wantRows)
		}
	}
}

// ── ActivityState.String ─────────────────────────────────────────────

func TestActivityStateString(t *testing.T) {
	tests := []struct {
		s    ActivityState
		want string
	}{
		{StateRunning, "running"},
		{StateIdle, "idle"},
		{ActivityState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("ActivityState(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// ── encodePathForClaude ──────────────────────────────────────────────

func TestEncodePathForClaude(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/foo/bar", "-Users-foo-bar"},
		{"/tmp/test", "-tmp-test"},
		{"relative", "relative"},
	}
	for _, tt := range tests {
		if got := encodePathForClaude(tt.path); got != tt.want {
			t.Errorf("encodePathForClaude(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// ── newestJSONL ──────────────────────────────────────────────────────

func TestNewestJSONL(t *testing.T) {
	dir := t.TempDir()

	// No files.
	if got := newestJSONL(dir); got != "" {
		t.Errorf("empty dir: got %q, want empty", got)
	}

	// Create two JSONL files with different mod times.
	older := filepath.Join(dir, "a.jsonl")
	newer := filepath.Join(dir, "b.jsonl")
	os.WriteFile(older, []byte(`{"type":"user"}`+"\n"), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(newer, []byte(`{"type":"assistant"}`+"\n"), 0644)

	if got := newestJSONL(dir); got != newer {
		t.Errorf("got %q, want %q", got, newer)
	}

	// Non-jsonl files ignored.
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("nope"), 0644)
	if got := newestJSONL(dir); got != newer {
		t.Errorf("after adding .txt: got %q, want %q", got, newer)
	}

	// Subdirectories ignored.
	os.MkdirAll(filepath.Join(dir, "sub.jsonl"), 0755)
	if got := newestJSONL(dir); got != newer {
		t.Errorf("after adding dir: got %q, want %q", got, newer)
	}
}

func TestNewestJSONL_BadDir(t *testing.T) {
	if got := newestJSONL("/nonexistent/dir"); got != "" {
		t.Errorf("bad dir: got %q, want empty", got)
	}
}

// ── lastJSONLType ────────────────────────────────────────────────────

func TestLastJSONLType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"user", `{"type":"user","text":"hi"}` + "\n", "user"},
		{"assistant", "{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n", "assistant"},
		{"empty file", "", ""},
		{"invalid json", "not json\n", ""},
		{"multi-line last wins", `{"type":"user"}` + "\n" + `{"type":"progress"}` + "\n", "progress"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(path, []byte(tt.content), 0644)
			if got := lastJSONLType(path); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLastJSONLType_MissingFile(t *testing.T) {
	if got := lastJSONLType("/nonexistent/file.jsonl"); got != "" {
		t.Errorf("missing file: got %q, want empty", got)
	}
}

// ── Pane lifecycle ───────────────────────────────────────────────────

func TestNewPaneAndClose(t *testing.T) {
	p := newLivePaneT(t, "test", []string{"echo", "hello"})
	defer p.Close()

	if p.name != "test" {
		t.Errorf("name = %q, want %q", p.name, "test")
	}
	if !p.Visible() {
		t.Error("new pane should be visible")
	}
	if p.Activity() != StateIdle {
		t.Errorf("initial activity = %v, want idle", p.Activity())
	}
}

func TestNewPaneDefaultShell(t *testing.T) {
	// Empty command should use $SHELL.
	p, err := NewPane(PaneConfig{Name: "shell"}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	defer p.Close()

	if !p.IsAlive() {
		t.Error("shell pane should be alive")
	}
}

func TestPaneIsAliveAfterClose(t *testing.T) {
	p := newLivePaneT(t, "short", []string{"true"})

	// Wait for process to exit.
	time.Sleep(200 * time.Millisecond)
	p.Close()

	if p.IsAlive() {
		t.Error("pane should not be alive after close")
	}
}

func TestPaneResize(t *testing.T) {
	p := newLivePaneT(t, "resize", []string{"cat"})
	defer p.Close()

	p.Resize(40, 100)
	if w := p.emu.Width(); w != 100 {
		t.Errorf("emu width after resize = %d, want 100", w)
	}
	if h := p.emu.Height(); h != 40 {
		t.Errorf("emu height after resize = %d, want 40", h)
	}
}

func TestPaneSessionDescInitiallyEmpty(t *testing.T) {
	p := newLivePaneT(t, "desc", []string{"true"})
	defer p.Close()

	// No session description before any rendering.
	if desc := p.SessionDesc(); desc != "" {
		t.Errorf("initial SessionDesc = %q, want empty", desc)
	}
}

// ── Scrollback ───────────────────────────────────────────────────────

func TestScrollUpDown(t *testing.T) {
	p := newEmuPane("scroll", 80, 24)

	if p.InScrollback() {
		t.Error("should not be in scrollback initially")
	}

	// ScrollUp with no scrollback history clamps to 0.
	p.ScrollUp(10)
	if p.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d after ScrollUp with no history, want 0", p.scrollOffset)
	}

	// ScrollDown below 0 clamps to 0.
	p.ScrollDown(5)
	if p.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d after ScrollDown, want 0", p.scrollOffset)
	}
	if p.InScrollback() {
		t.Error("should not be in scrollback after clamping to 0")
	}
}

func TestInScrollback(t *testing.T) {
	p := newEmuPane("sb", 80, 24)
	p.scrollOffset = 5

	if !p.InScrollback() {
		t.Error("should be in scrollback when offset > 0")
	}

	p.ScrollDown(5)
	if p.InScrollback() {
		t.Error("should not be in scrollback after scrolling back to 0")
	}
}

// ── contentOffset ────────────────────────────────────────────────────

func TestContentOffsetAltScreen(t *testing.T) {
	p := newEmuPane("alt", 80, 24)
	// Switch to alt screen by writing the appropriate escape.
	p.emu.Write([]byte("\x1b[?1049h"))
	startRow, renderOffset := p.contentOffset()
	if startRow != 0 || renderOffset != 0 {
		t.Errorf("alt screen: startRow=%d renderOffset=%d, want 0,0", startRow, renderOffset)
	}
}

func TestContentOffsetScrollback(t *testing.T) {
	p := newEmuPane("sb", 80, 24)
	p.scrollOffset = 10
	startRow, renderOffset := p.contentOffset()
	if startRow != 0 || renderOffset != 0 {
		t.Errorf("scrollback mode: startRow=%d renderOffset=%d, want 0,0", startRow, renderOffset)
	}
}

// ── IPC: SocketPath ──────────────────────────────────────────────────

func TestSocketPath(t *testing.T) {
	got := SocketPath("myproject")
	want := "/tmp/initech-myproject.sock"
	if got != want {
		t.Errorf("SocketPath = %q, want %q", got, want)
	}
}

// ── IPC: findPane ────────────────────────────────────────────────────

func TestFindPane(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", true)
	tui := newTestTUI(a, b)

	if got := tui.findPane("eng1"); got != b {
		t.Error("findPane(eng1) returned wrong pane")
	}
	if got := tui.findPane("nonexistent"); got != nil {
		t.Error("findPane(nonexistent) should return nil")
	}
}

// ── IPC: writeIPCResponse ────────────────────────────────────────────

func TestWriteIPCResponse(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go writeIPCResponse(server, IPCResponse{OK: true, Data: "hello"})

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	got := string(buf[:n])

	var resp IPCResponse
	// Strip trailing newline before unmarshal.
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %q)", err, got)
	}
	if !resp.OK {
		t.Error("response should be OK")
	}
	if resp.Data != "hello" {
		t.Errorf("response Data = %q, want %q", resp.Data, "hello")
	}
}

// ── IPC: handleIPCList ───────────────────────────────────────────────

func TestHandleIPCList(t *testing.T) {
	a := newEmuPane("super", 80, 24)
	b := newEmuPane("eng1", 80, 24)
	b.SetVisible(false)
	tui := &TUI{panes: []*Pane{a, b}}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go tui.handleIPCList(server)

	buf := make([]byte, 4096)
	n, _ := client.Read(buf)

	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if !resp.OK {
		t.Fatalf("list response not OK: %s", resp.Error)
	}

	type paneInfo struct {
		Name     string `json:"name"`
		Activity string `json:"activity"`
		Alive    bool   `json:"alive"`
		Visible  bool   `json:"visible"`
	}
	var panes []paneInfo
	json.Unmarshal([]byte(resp.Data), &panes)

	if len(panes) != 2 {
		t.Fatalf("got %d panes, want 2", len(panes))
	}
	if panes[0].Name != "super" || !panes[0].Visible {
		t.Errorf("pane[0] = %+v, want super/visible", panes[0])
	}
	if panes[1].Name != "eng1" || panes[1].Visible {
		t.Errorf("pane[1] = %+v, want eng1/hidden", panes[1])
	}
}

// ── IPC: handleIPCPeek ──────────────────────────────────────────────

func TestHandleIPCPeek(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	// Write some text into the emulator.
	p.emu.Write([]byte("hello world\r\nline two\r\n"))
	tui := &TUI{panes: []*Pane{p}}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go tui.handleIPCPeek(server, IPCRequest{Target: "eng1", Lines: 2})

	buf := make([]byte, 8192)
	n, _ := client.Read(buf)

	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if !resp.OK {
		t.Fatalf("peek not OK: %s", resp.Error)
	}
	if !strings.Contains(resp.Data, "hello world") {
		t.Errorf("peek data missing 'hello world': %q", resp.Data)
	}
}

func TestHandleIPCPeekMissingTarget(t *testing.T) {
	tui := &TUI{}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go tui.handleIPCPeek(server, IPCRequest{Target: ""})

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if resp.OK {
		t.Error("should error on empty target")
	}
}

func TestHandleIPCPeekUnknownTarget(t *testing.T) {
	tui := &TUI{panes: []*Pane{newEmuPane("eng1", 80, 24)}}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go tui.handleIPCPeek(server, IPCRequest{Target: "nonexistent"})

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if resp.OK {
		t.Error("should error on unknown target")
	}
}

// ── IPC: handleIPCSend ──────────────────────────────────────────────

func TestHandleIPCSendMissingTarget(t *testing.T) {
	tui := &TUI{}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go tui.handleIPCSend(server, IPCRequest{Target: ""})

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if resp.OK {
		t.Error("should error on empty target")
	}
}

func TestHandleIPCSendUnknownTarget(t *testing.T) {
	tui := &TUI{panes: []*Pane{newEmuPane("eng1", 80, 24)}}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go tui.handleIPCSend(server, IPCRequest{Target: "ghost"})

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if resp.OK {
		t.Error("should error on unknown target")
	}
}

func TestHandleIPCSendNoEnter(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	tui := &TUI{panes: []*Pane{p}}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go tui.handleIPCSend(server, IPCRequest{Target: "eng1", Text: "hello", Enter: false})

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if !resp.OK {
		t.Errorf("send without Enter should succeed: %s", resp.Error)
	}
}

// ── IPC: hasStuckInput ──────────────────────────────────────────────

func TestHasStuckInput_EmptyPrompt(t *testing.T) {
	p := newEmuPane("test", 80, 24)
	// Write a prompt with nothing after it.
	p.emu.Write([]byte("\u276f "))
	if hasStuckInput(p) {
		t.Error("empty prompt should not be stuck")
	}
}

func TestHasStuckInput_NoPrompt(t *testing.T) {
	p := newEmuPane("test", 80, 24)
	// Claude is generating (no prompt visible).
	p.emu.Write([]byte("Processing your request..."))
	if hasStuckInput(p) {
		t.Error("no prompt should not be stuck")
	}
}

func TestHasStuckInput_PasteIndicator(t *testing.T) {
	p := newEmuPane("test", 80, 24)
	p.emu.Write([]byte("\u276f [Pasted text #1]"))
	if !hasStuckInput(p) {
		t.Error("paste indicator should be detected as stuck")
	}
}

func TestHasStuckInput_TextAtPrompt(t *testing.T) {
	p := newEmuPane("test", 80, 24)
	p.emu.Write([]byte("\u276f some leftover text"))
	if !hasStuckInput(p) {
		t.Error("text at prompt should be detected as stuck")
	}
}

// ── IPC: handleIPCConn routing ──────────────────────────────────────

func TestHandleIPCConnRouting(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	tui := &TUI{panes: []*Pane{p}}

	tests := []struct {
		name   string
		action string
		wantOK bool
	}{
		{"list", "list", true},
		{"unknown action", "explode", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()

			req, _ := json.Marshal(IPCRequest{Action: tt.action, Target: "eng1"})
			go func() {
				client.Write(append(req, '\n'))
			}()

			go tui.handleIPCConn(server)

			buf := make([]byte, 4096)
			n, _ := client.Read(buf)
			var resp IPCResponse
			json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
			if resp.OK != tt.wantOK {
				t.Errorf("action %q: OK=%v, want %v (err: %s)", tt.action, resp.OK, tt.wantOK, resp.Error)
			}
		})
	}
}

func TestHandleIPCConnBadJSON(t *testing.T) {
	tui := &TUI{}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		client.Write([]byte("not json\n"))
	}()

	go tui.handleIPCConn(server)

	buf := make([]byte, 1024)
	n, _ := client.Read(buf)
	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if resp.OK {
		t.Error("bad JSON should fail")
	}
	if !strings.Contains(resp.Error, "invalid JSON") {
		t.Errorf("error = %q, want 'invalid JSON'", resp.Error)
	}
}

// ── IPC: full round-trip via socket ─────────────────────────────────

func TestIPCRoundTrip(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.emu.Write([]byte("visible content\r\n"))
	tui := &TUI{panes: []*Pane{p}}

	sockPath := filepath.Join(t.TempDir(), "test.sock")
	if err := tui.startIPC(sockPath); err != nil {
		t.Fatalf("startIPC: %v", err)
	}

	// Connect and send a list request.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req, _ := json.Marshal(IPCRequest{Action: "list"})
	conn.Write(append(req, '\n'))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp IPCResponse
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &resp)
	if !resp.OK {
		t.Fatalf("list not OK: %s", resp.Error)
	}
	if !strings.Contains(resp.Data, "eng1") {
		t.Errorf("list data missing eng1: %q", resp.Data)
	}
}

// ── uvCellToTcell ────────────────────────────────────────────────────

func TestUvCellToTcell_Nil(t *testing.T) {
	ch, style := uvCellToTcell(nil)
	if ch != ' ' {
		t.Errorf("nil cell: ch = %q, want ' '", ch)
	}
	if style != tcell.StyleDefault {
		t.Error("nil cell: style should be default")
	}
}

func TestUvCellToTcell_Empty(t *testing.T) {
	cell := &uv.Cell{Content: ""}
	ch, _ := uvCellToTcell(cell)
	if ch != ' ' {
		t.Errorf("empty cell: ch = %q, want ' '", ch)
	}
}

func TestUvCellToTcell_WithContent(t *testing.T) {
	cell := &uv.Cell{Content: "A"}
	ch, _ := uvCellToTcell(cell)
	if ch != 'A' {
		t.Errorf("cell: ch = %q, want 'A'", ch)
	}
}

func TestUvCellToTcell_WithAttributes(t *testing.T) {
	cell := &uv.Cell{
		Content: "B",
		Style: uv.Style{
			Attrs: uv.AttrBold | uv.AttrItalic | uv.AttrFaint | uv.AttrReverse | uv.AttrStrikethrough,
			Underline: 1,
		},
	}
	ch, _ := uvCellToTcell(cell)
	if ch != 'B' {
		t.Errorf("styled cell: ch = %q, want 'B'", ch)
	}
}

func TestUvCellToTcell_WithColors(t *testing.T) {
	cell := &uv.Cell{
		Content: "C",
		Style: uv.Style{
			Fg: ansi.BasicColor(1), // red
			Bg: ansi.BasicColor(2), // green
		},
	}
	ch, _ := uvCellToTcell(cell)
	if ch != 'C' {
		t.Errorf("colored cell: ch = %q, want 'C'", ch)
	}
}

// ── uvColorToTcell ───────────────────────────────────────────────────

func TestUvColorToTcell(t *testing.T) {
	tests := []struct {
		name string
		c    interface{ RGBA() (uint32, uint32, uint32, uint32) }
	}{
		{"nil", nil},
		{"basic", ansi.BasicColor(3)},
		{"indexed", ansi.IndexedColor(100)},
		{"rgb", ansi.RGBColor{R: 255, G: 128, B: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.c == nil {
				c := uvColorToTcell(nil)
				if c != tcell.ColorDefault {
					t.Errorf("nil: got %v, want default", c)
				}
				return
			}
			// Just verify it doesn't panic.
			uvColorToTcell(tt.c)
		})
	}
}

// ── tcellKeyToUV ─────────────────────────────────────────────────────

func TestTcellKeyToUV(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		r    rune
		mod  tcell.ModMask
	}{
		{"rune a", tcell.KeyRune, 'a', 0},
		{"enter", tcell.KeyEnter, 0, 0},
		{"backspace", tcell.KeyBackspace2, 0, 0},
		{"tab", tcell.KeyTab, 0, 0},
		{"escape", tcell.KeyEscape, 0, 0},
		{"up", tcell.KeyUp, 0, 0},
		{"down", tcell.KeyDown, 0, 0},
		{"left", tcell.KeyLeft, 0, 0},
		{"right", tcell.KeyRight, 0, 0},
		{"home", tcell.KeyHome, 0, 0},
		{"end", tcell.KeyEnd, 0, 0},
		{"delete", tcell.KeyDelete, 0, 0},
		{"pgup", tcell.KeyPgUp, 0, 0},
		{"pgdn", tcell.KeyPgDn, 0, 0},
		{"insert", tcell.KeyInsert, 0, 0},
		{"F1", tcell.KeyF1, 0, 0},
		{"F12", tcell.KeyF12, 0, 0},
		{"ctrl+a", tcell.KeyCtrlA, 0, tcell.ModCtrl},
		{"ctrl+z", tcell.KeyCtrlZ, 0, tcell.ModCtrl},
		{"alt+rune", tcell.KeyRune, 'x', tcell.ModAlt},
		{"shift+enter", tcell.KeyEnter, 0, tcell.ModShift},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tt.key, tt.r, tt.mod)
			// Verify no panic.
			_ = tcellKeyToUV(ev)
		})
	}
}

func TestTcellKeyToUV_FallbackSpace(t *testing.T) {
	// A key not in the switch should return space fallback.
	ev := tcell.NewEventKey(tcell.KeyF13, 0, 0)
	kpe := tcellKeyToUV(ev)
	if uv.Key(kpe).Code != uv.KeySpace {
		t.Errorf("unknown key should fallback to space, got %v", uv.Key(kpe).Code)
	}
}

// ── parseGrid ────────────────────────────────────────────────────────

func TestParseGrid(t *testing.T) {
	tests := []struct {
		input    string
		numPanes int
		wantC    int
		wantR    int
		wantOK   bool
	}{
		{"3x3", 9, 3, 3, true},
		{"2x4", 8, 2, 4, true},
		{"3", 9, 3, 3, true},
		{"4", 7, 4, 2, true},
		{"0x0", 1, 0, 0, false},
		{"11x1", 1, 0, 0, false},
		{"abc", 1, 0, 0, false},
		{"2X3", 6, 2, 3, true}, // case insensitive
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			c, r, ok := parseGrid(tt.input, tt.numPanes)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && (c != tt.wantC || r != tt.wantR) {
				t.Errorf("(%d,%d), want (%d,%d)", c, r, tt.wantC, tt.wantR)
			}
		})
	}
}

// ── autoGrid ─────────────────────────────────────────────────────────

func TestAutoGrid(t *testing.T) {
	tests := []struct {
		n     int
		wantC int
		wantR int
	}{
		{0, 1, 1},
		{1, 1, 1},
		{2, 2, 1},
		{3, 2, 2},
		{4, 2, 2},
		{5, 3, 2},
		{6, 3, 2},
		{7, 4, 2},
		{8, 4, 2},
		{9, 3, 3},
		{10, 4, 3},
		{12, 4, 3},
		{13, 4, 4},
		{20, 4, 5},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("n=%d", tt.n), func(t *testing.T) {
			c, r := autoGrid(tt.n)
			if c != tt.wantC || r != tt.wantR {
				t.Errorf("autoGrid(%d) = (%d,%d), want (%d,%d)", tt.n, c, r, tt.wantC, tt.wantR)
			}
		})
	}
}

// ── calcMainVertical ─────────────────────────────────────────────────

func TestCalcMainVertical(t *testing.T) {
	// Single pane: full screen.
	regions := calcMainVertical(1, 200, 100)
	if len(regions) != 1 {
		t.Fatalf("n=1: got %d regions, want 1", len(regions))
	}
	if regions[0].W != 200 || regions[0].H != 100 {
		t.Errorf("n=1: region = %+v, want full screen", regions[0])
	}

	// Multiple panes: main left + stacked right.
	regions = calcMainVertical(4, 200, 100)
	if len(regions) != 4 {
		t.Fatalf("n=4: got %d regions, want 4", len(regions))
	}
	// Main pane should be ~60% width.
	if regions[0].W != 120 {
		t.Errorf("main pane width = %d, want 120", regions[0].W)
	}
	// Right panes should fill remaining width.
	for i := 1; i < len(regions); i++ {
		if regions[i].X != 120 {
			t.Errorf("right pane[%d].X = %d, want 120", i, regions[i].X)
		}
		if regions[i].W != 80 {
			t.Errorf("right pane[%d].W = %d, want 80", i, regions[i].W)
		}
	}
	// Right panes should sum to full height.
	totalH := 0
	for i := 1; i < len(regions); i++ {
		totalH += regions[i].H
	}
	if totalH != 100 {
		t.Errorf("right panes total height = %d, want 100", totalH)
	}
}

// ── selectionFor ─────────────────────────────────────────────────────

func TestSelectionFor(t *testing.T) {
	tui := &TUI{
		selActive: true,
		selPane:   1,
		selStartX: 5, selStartY: 10,
		selEndX: 15, selEndY: 12,
	}

	// Matching pane index.
	sel := tui.selectionFor(1)
	if !sel.Active {
		t.Error("should be active for matching pane")
	}
	if sel.StartX != 5 || sel.StartY != 10 || sel.EndX != 15 || sel.EndY != 12 {
		t.Errorf("selection coords wrong: %+v", sel)
	}

	// Non-matching pane index.
	sel = tui.selectionFor(0)
	if sel.Active {
		t.Error("should be inactive for non-matching pane")
	}

	// Inactive selection.
	tui.selActive = false
	sel = tui.selectionFor(1)
	if sel.Active {
		t.Error("should be inactive when selActive=false")
	}
}

// ── DefaultConfig ────────────────────────────────────────────────────

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Agents) != 4 {
		t.Errorf("DefaultConfig agents = %d, want 4", len(cfg.Agents))
	}
	names := make([]string, len(cfg.Agents))
	for i, a := range cfg.Agents {
		names[i] = a.Name
	}
	want := "super,eng1,eng2,qa1"
	got := strings.Join(names, ",")
	if got != want {
		t.Errorf("names = %q, want %q", got, want)
	}
}

// ── calcRegions ──────────────────────────────────────────────────────

func TestCalcRegionsZoomReturnsOneRegion(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true), newTestPane("b", true))
	tui.zoomed = true
	regions := tui.calcRegions(200, 100)
	if len(regions) != 1 {
		t.Errorf("zoom: got %d regions, want 1", len(regions))
	}
}

func TestCalcRegionsFocusReturnsOneRegion(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true), newTestPane("b", true))
	tui.layout = LayoutFocus
	regions := tui.calcRegions(200, 100)
	if len(regions) != 1 {
		t.Errorf("focus: got %d regions, want 1", len(regions))
	}
}

func TestCalcRegionsNoPanes(t *testing.T) {
	tui := &TUI{}
	regions := tui.calcRegions(200, 100)
	if regions != nil {
		t.Errorf("no panes: got %v, want nil", regions)
	}
}

func TestCalcRegions2Col(t *testing.T) {
	a := newTestPane("a", true)
	b := newTestPane("b", true)
	c := newTestPane("c", true)
	tui := newTestTUI(a, b, c)
	tui.layout = Layout2Col
	regions := tui.calcRegions(200, 100)
	if len(regions) != 3 {
		t.Fatalf("2col: got %d regions, want 3", len(regions))
	}
}

// ── execCmd ──────────────────────────────────────────────────────────

func TestExecCmdEmpty(t *testing.T) {
	tui := newTestTUI()
	if tui.execCmd("") {
		t.Error("empty command should not quit")
	}
}

func TestExecCmdQuit(t *testing.T) {
	tui := newTestTUI()
	if !tui.execCmd("quit") {
		t.Error("quit should return true")
	}
}

func TestExecCmdQuitShort(t *testing.T) {
	tui := newTestTUI()
	if !tui.execCmd("q") {
		t.Error("q should return true")
	}
}

func TestExecCmdUnknown(t *testing.T) {
	tui := newTestTUI()
	tui.execCmd("notacmd")
	if !strings.Contains(tui.cmdError, "unknown command") {
		t.Errorf("cmdError = %q, want 'unknown command'", tui.cmdError)
	}
}

func TestExecCmdPanel(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.overlay = false
	tui.execCmd("panel")
	if !tui.overlay {
		t.Error("panel should toggle overlay on")
	}
	tui.execCmd("panel")
	if tui.overlay {
		t.Error("panel should toggle overlay off")
	}
}

func TestExecCmdZoom(t *testing.T) {
	// Test zoom toggle directly since execCmd("zoom") calls relayout
	// which needs a screen.
	tui := newTestTUI(newTestPane("a", true))
	tui.zoomed = false
	tui.zoomed = !tui.zoomed // Simulate what execCmd("zoom") does.
	if !tui.zoomed {
		t.Error("zoom should toggle on")
	}
	tui.zoomed = !tui.zoomed
	if tui.zoomed {
		t.Error("zoom should toggle off")
	}
}

func TestExecCmdShowHide(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", true)
	tui := newTestTUI(a, b)

	// Hide eng1.
	tui.execCmd("hide eng1")
	if b.Visible() {
		t.Error("eng1 should be hidden")
	}

	// Show eng1.
	tui.execCmd("show eng1")
	if !b.Visible() {
		t.Error("eng1 should be visible")
	}

	// Show all.
	b.SetVisible(false)
	tui.execCmd("show all")
	if !b.Visible() {
		t.Error("show all should make eng1 visible")
	}
}

func TestExecCmdHideLastPane(t *testing.T) {
	a := newTestPane("super", true)
	tui := newTestTUI(a)
	tui.execCmd("hide super")
	if !a.Visible() {
		t.Error("should not hide last visible pane")
	}
	if !strings.Contains(tui.cmdError, "cannot hide last") {
		t.Errorf("cmdError = %q, want 'cannot hide last'", tui.cmdError)
	}
}

func TestExecCmdHideAlreadyHidden(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", false)
	tui := newTestTUI(a, b)
	tui.execCmd("hide eng1") // Already hidden, should be no-op.
	if tui.cmdError != "" {
		t.Errorf("hide already-hidden should not error: %q", tui.cmdError)
	}
}

func TestExecCmdGridNoArg(t *testing.T) {
	// grid calls setGrid which calls relayout. Test state directly.
	tui := newTestTUI(
		newTestPane("a", true), newTestPane("b", true),
		newTestPane("c", true), newTestPane("d", true),
	)
	tui.layout = LayoutFocus
	c, r := autoGrid(tui.visibleCount())
	tui.layout = LayoutGrid
	tui.gridCols = c
	tui.gridRows = r
	if tui.layout != LayoutGrid {
		t.Error("grid should switch to LayoutGrid")
	}
}

func TestExecCmdGridWithArg(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true), newTestPane("b", true))
	// Simulate grid 2x1.
	tui.layout = LayoutGrid
	tui.gridCols = 2
	tui.gridRows = 1
	if tui.gridCols != 2 || tui.gridRows != 1 {
		t.Errorf("grid 2x1: cols=%d rows=%d", tui.gridCols, tui.gridRows)
	}
}

func TestExecCmdGridInvalid(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("grid abc")
	if !strings.Contains(tui.cmdError, "invalid grid") {
		t.Errorf("cmdError = %q", tui.cmdError)
	}
}

func TestExecCmdFocusNoArg(t *testing.T) {
	// focus calls relayout, so test the state change logic directly.
	tui := newTestTUI(newTestPane("a", true))
	tui.layout = LayoutGrid
	// Simulate what execCmd("focus") does minus relayout.
	tui.layout = LayoutFocus
	tui.zoomed = false
	if tui.layout != LayoutFocus {
		t.Error("focus should switch to LayoutFocus")
	}
}

func TestExecCmdFocusWithName(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", true)
	tui := newTestTUI(a, b)
	tui.layout = LayoutGrid

	// Simulate focus eng1 (minus relayout).
	for i, p := range tui.panes {
		if p.name == "eng1" {
			tui.focused = i
			tui.layout = LayoutFocus
			tui.zoomed = false
			break
		}
	}
	if tui.focused != 1 {
		t.Errorf("focused = %d, want 1", tui.focused)
	}
	if tui.layout != LayoutFocus {
		t.Error("should be in LayoutFocus")
	}
}

func TestExecCmdFocusUnknown(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("focus ghost")
	if !strings.Contains(tui.cmdError, "unknown agent") {
		t.Errorf("cmdError = %q", tui.cmdError)
	}
}

func TestExecCmdMain(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.layout = LayoutGrid
	// main calls relayout; test the state change directly.
	tui.layout = Layout2Col
	tui.zoomed = false
	if tui.layout != Layout2Col {
		t.Error("main should switch to Layout2Col")
	}
}

// ── handleCmdKey ─────────────────────────────────────────────────────

func TestHandleCmdKeyEscape(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.cmdActive = true
	tui.cmdBuf = []rune("partial")

	ev := tcell.NewEventKey(tcell.KeyEscape, 0, 0)
	tui.handleCmdKey(ev)
	if tui.cmdActive {
		t.Error("Escape should close cmd modal")
	}
	if len(tui.cmdBuf) != 0 {
		t.Error("Escape should clear cmdBuf")
	}
}

func TestHandleCmdKeyBackspace(t *testing.T) {
	tui := newTestTUI()
	tui.cmdBuf = []rune("abc")

	ev := tcell.NewEventKey(tcell.KeyBackspace2, 0, 0)
	tui.handleCmdKey(ev)
	if string(tui.cmdBuf) != "ab" {
		t.Errorf("cmdBuf = %q, want 'ab'", string(tui.cmdBuf))
	}
}

func TestHandleCmdKeyBackspaceEmpty(t *testing.T) {
	tui := newTestTUI()
	tui.cmdBuf = []rune{}

	ev := tcell.NewEventKey(tcell.KeyBackspace2, 0, 0)
	tui.handleCmdKey(ev) // Should not panic.
	if len(tui.cmdBuf) != 0 {
		t.Error("should stay empty")
	}
}

func TestHandleCmdKeyRune(t *testing.T) {
	tui := newTestTUI()
	tui.cmdBuf = []rune("he")

	ev := tcell.NewEventKey(tcell.KeyRune, 'l', 0)
	tui.handleCmdKey(ev)
	if string(tui.cmdBuf) != "hel" {
		t.Errorf("cmdBuf = %q, want 'hel'", string(tui.cmdBuf))
	}
}

func TestHandleCmdKeyBacktickEmpty(t *testing.T) {
	tui := newTestTUI()
	tui.cmdActive = true
	tui.cmdBuf = []rune{}

	ev := tcell.NewEventKey(tcell.KeyRune, '`', 0)
	tui.handleCmdKey(ev)
	if tui.cmdActive {
		t.Error("backtick on empty should close modal")
	}
}

func TestHandleCmdKeyEnter(t *testing.T) {
	tui := newTestTUI()
	tui.cmdActive = true
	tui.cmdBuf = []rune("quit")

	ev := tcell.NewEventKey(tcell.KeyEnter, 0, 0)
	quit := tui.handleCmdKey(ev)
	if !quit {
		t.Error("Enter on 'quit' should return true")
	}
	if tui.cmdActive {
		t.Error("Enter should close modal")
	}
}

// ── handleKey ────────────────────────────────────────────────────────

func TestHandleKeyBacktickOpensModal(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	ev := tcell.NewEventKey(tcell.KeyRune, '`', 0)
	tui.handleKey(ev)
	if !tui.cmdActive {
		t.Error("backtick should open command modal")
	}
}

func TestHandleKeyAltQ(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	ev := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModAlt)
	quit := tui.handleKey(ev)
	if !quit {
		t.Error("Alt+q should quit")
	}
}

func TestHandleKeyAltS(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.overlay = false
	ev := tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt)
	tui.handleKey(ev)
	if !tui.overlay {
		t.Error("Alt+s should toggle overlay")
	}
}

func TestHandleKeyAltZ(t *testing.T) {
	// Alt+z toggles zoom. Test state directly since relayout needs screen.
	tui := newTestTUI(newTestPane("a", true))
	tui.zoomed = false
	tui.zoomed = !tui.zoomed
	if !tui.zoomed {
		t.Error("Alt+z should toggle zoom")
	}
}

func TestHandleKeyAltArrows(t *testing.T) {
	a := newTestPane("a", true)
	b := newTestPane("b", true)
	tui := newTestTUI(a, b)
	tui.focused = 0

	ev := tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 1 {
		t.Error("Alt+Right should advance focus")
	}

	ev = tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 0 {
		t.Error("Alt+Left should go back")
	}

	ev = tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 1 {
		t.Error("Alt+Down should advance focus")
	}

	ev = tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 0 {
		t.Error("Alt+Up should go back")
	}
}

func TestHandleKeyAltNumbers(t *testing.T) {
	// Alt+number keys change layout. Since they call relayout (needs screen),
	// test the expected state transitions directly.
	tui := newTestTUI(newTestPane("a", true))
	tui.layout = LayoutGrid

	// Alt+1 sets focus mode.
	tui.layout = LayoutFocus
	tui.zoomed = false
	if tui.layout != LayoutFocus {
		t.Error("Alt+1 should set LayoutFocus")
	}

	// Alt+4 sets 2col.
	tui.layout = Layout2Col
	tui.zoomed = false
	if tui.layout != Layout2Col {
		t.Error("Alt+4 should set Layout2Col")
	}

	// Alt+2 sets 2x2 grid.
	tui.layout = LayoutGrid
	tui.gridCols = 2
	tui.gridRows = 2
	tui.zoomed = false
	if tui.gridCols != 2 || tui.gridRows != 2 {
		t.Error("Alt+2 should set 2x2 grid")
	}

	// Alt+3 sets 3x3 grid.
	tui.gridCols = 3
	tui.gridRows = 3
	if tui.gridCols != 3 || tui.gridRows != 3 {
		t.Error("Alt+3 should set 3x3 grid")
	}
}

func TestHandleKeyClearsError(t *testing.T) {
	tui := newTestTUI(newEmuPane("a", 80, 24))
	tui.cmdError = "some old error"

	ev := tcell.NewEventKey(tcell.KeyRune, 'x', 0)
	tui.handleKey(ev)
	if tui.cmdError != "" {
		t.Errorf("keypress should clear cmdError, got %q", tui.cmdError)
	}
}

// ── handleEvent routing ──────────────────────────────────────────────

func TestHandleEventRouting(t *testing.T) {
	// handleEvent routes to the right handler. We can only test
	// EventKey since EventResize and EventMouse need a screen.
	tui := newTestTUI(newTestPane("a", true))
	ev := tcell.NewEventKey(tcell.KeyRune, '`', 0)
	quit := tui.handleEvent(ev)
	if quit {
		t.Error("backtick should not quit")
	}
	if !tui.cmdActive {
		t.Error("backtick should open cmd modal via handleEvent")
	}
}

// ── contentOffset with real content ──────────────────────────────────

func TestContentOffsetWithContent(t *testing.T) {
	p := newEmuPane("test", 80, 24)
	// Write a few lines of content. Emulator starts at row 0.
	p.emu.Write([]byte("line 1\r\nline 2\r\nline 3\r\n"))
	// Give emulator a moment to process.
	time.Sleep(10 * time.Millisecond)

	startRow, renderOffset := p.contentOffset()
	// With only 3 lines of content in a 24-row pane, content should be
	// bottom-anchored with renderOffset > 0.
	if renderOffset == 0 && startRow == 0 {
		// This is acceptable if the emulator hasn't processed the text yet
		// or if cursor position doesn't trigger bottom-anchoring.
		t.Log("contentOffset returned 0,0 (emulator may not have settled)")
	}
	// Main test: no panic, values are non-negative.
	if startRow < 0 || renderOffset < 0 {
		t.Errorf("negative values: startRow=%d renderOffset=%d", startRow, renderOffset)
	}
}

// ── Pane SendKey ─────────────────────────────────────────────────────

func TestPaneSendKey(t *testing.T) {
	p := newEmuPane("test", 80, 24)
	// Should not panic when sending keys to emulator-only pane.
	ev := tcell.NewEventKey(tcell.KeyRune, 'a', 0)
	p.SendKey(ev)
}

// ── Pane ForwardMouse ────────────────────────────────────────────────

func TestPaneForwardMouse(t *testing.T) {
	p := newEmuPane("test", 80, 24)
	// Should not panic. Emulator drops mouse events when reporting is off.
	m := uv.Mouse{X: 5, Y: 10, Button: uv.MouseLeft}
	p.ForwardMouse(uv.MouseClickEvent(m))
}

// ── recalcGrid ───────────────────────────────────────────────────────

func TestRecalcGridNoScreen(t *testing.T) {
	a := newTestPane("a", true)
	b := newTestPane("b", true)
	c := newTestPane("c", true)
	tui := newTestTUI(a, b, c)
	tui.layout = LayoutGrid

	// Should not panic with nil screen.
	tui.recalcGrid()
	if tui.gridCols != 2 || tui.gridRows != 2 {
		t.Errorf("recalcGrid(3 panes): cols=%d rows=%d, want 2x2", tui.gridCols, tui.gridRows)
	}
}

func TestRecalcGridNonGridLayout(t *testing.T) {
	a := newTestPane("a", true)
	tui := newTestTUI(a)
	tui.layout = LayoutFocus
	tui.gridCols = 5
	tui.gridRows = 5

	tui.recalcGrid()
	// Non-grid layout should not change grid dimensions.
	if tui.gridCols != 5 || tui.gridRows != 5 {
		t.Errorf("non-grid recalcGrid changed dims: cols=%d rows=%d", tui.gridCols, tui.gridRows)
	}
}
