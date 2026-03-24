package tmux

import (
	"errors"
	"strings"
	"testing"

	iexec "github.com/nmelo/initech/internal/exec"
)

func TestSessionExists_True(t *testing.T) {
	fake := &iexec.FakeRunner{}
	if !SessionExists(fake, "myproject") {
		t.Error("expected true when tmux has-session succeeds")
	}
	if !strings.Contains(fake.Calls[0], "tmux has-session -t myproject") {
		t.Errorf("unexpected call: %s", fake.Calls[0])
	}
}

func TestSessionExists_False(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("no session")}
	if SessionExists(fake, "noexist") {
		t.Error("expected false when tmux has-session fails")
	}
}

func TestListWindows(t *testing.T) {
	fake := &iexec.FakeRunner{
		Output: "1|super|%1|1234|zsh\n2|eng1|%2|5678|node\n3|qa1|%3|9012|claude",
	}

	windows, err := ListWindows(fake, "project")
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("got %d windows, want 3", len(windows))
	}

	if windows[0].Name != "super" || windows[0].PanePID != "1234" || windows[0].Command != "zsh" {
		t.Errorf("window 0: %+v", windows[0])
	}
	if windows[1].Name != "eng1" || windows[1].Command != "node" {
		t.Errorf("window 1: %+v", windows[1])
	}
	if windows[2].Name != "qa1" || windows[2].Command != "claude" {
		t.Errorf("window 2: %+v", windows[2])
	}
}

func TestListWindows_Error(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("no session")}
	_, err := ListWindows(fake, "noexist")
	if err == nil {
		t.Error("expected error")
	}
}

func TestIsClaudeRunning_DirectNode(t *testing.T) {
	fake := &iexec.FakeRunner{}
	w := Window{Command: "node", PanePID: "123"}
	if !IsClaudeRunning(fake, w) {
		t.Error("should detect 'node' as Claude running")
	}
}

func TestIsClaudeRunning_DirectClaude(t *testing.T) {
	fake := &iexec.FakeRunner{}
	w := Window{Command: "claude", PanePID: "123"}
	if !IsClaudeRunning(fake, w) {
		t.Error("should detect 'claude' as Claude running")
	}
}

func TestIsClaudeRunning_VersionPattern(t *testing.T) {
	fake := &iexec.FakeRunner{}
	w := Window{Command: "2.1.45", PanePID: "123"}
	if !IsClaudeRunning(fake, w) {
		t.Error("should detect version pattern as Claude running")
	}
}

func TestIsClaudeRunning_ShellWithClaudeChild(t *testing.T) {
	fake := &iexec.FakeRunner{Output: "5678 node"}
	w := Window{Command: "zsh", PanePID: "1234"}
	if !IsClaudeRunning(fake, w) {
		t.Error("should detect Claude child under shell")
	}
}

func TestIsClaudeRunning_ShellNoChild(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("no children")}
	w := Window{Command: "zsh", PanePID: "1234"}
	if IsClaudeRunning(fake, w) {
		t.Error("should not detect Claude when pgrep finds no children")
	}
}

func TestIsClaudeRunning_OtherCommand(t *testing.T) {
	fake := &iexec.FakeRunner{}
	w := Window{Command: "vim", PanePID: "123"}
	if IsClaudeRunning(fake, w) {
		t.Error("should not detect vim as Claude")
	}
}

func TestGetClaudePID_DirectNode(t *testing.T) {
	fake := &iexec.FakeRunner{}
	w := Window{Command: "node", PanePID: "1234"}
	pid := GetClaudePID(fake, w)
	if pid != "1234" {
		t.Errorf("got %q, want %q", pid, "1234")
	}
}

func TestGetClaudePID_ShellChild(t *testing.T) {
	fake := &iexec.FakeRunner{Output: "5678 node"}
	w := Window{Command: "zsh", PanePID: "1234"}
	pid := GetClaudePID(fake, w)
	if pid != "5678" {
		t.Errorf("got %q, want %q", pid, "5678")
	}
}

func TestGetClaudePID_NotRunning(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("no children")}
	w := Window{Command: "zsh", PanePID: "1234"}
	pid := GetClaudePID(fake, w)
	if pid != "" {
		t.Errorf("got %q, want empty", pid)
	}
}

func TestGetProcessMemory(t *testing.T) {
	callCount := 0
	// Custom fake that returns different output per call
	fake := &fakeMultiRunner{
		responses: []struct {
			output string
			err    error
		}{
			{"  524288", nil},   // ps for main process (512 MB in KB)
			{"5679\n5680", nil}, // pgrep children
			{"  102400", nil},   // ps for child 5679 (100 MB in KB)
			{"  51200", nil},    // ps for child 5680 (50 MB in KB)
		},
	}
	_ = callCount

	w := Window{Command: "node", PanePID: "5678"}
	mem := GetProcessMemory(fake, w)

	// 524288 + 102400 + 51200 = 677888 KB = 677888 * 1024 bytes
	expected := uint64(677888) * 1024
	if mem != expected {
		t.Errorf("got %d, want %d", mem, expected)
	}
}

func TestGetProcessMemory_NotRunning(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("no children")}
	w := Window{Command: "zsh", PanePID: "1234"}
	mem := GetProcessMemory(fake, w)
	if mem != 0 {
		t.Errorf("got %d, want 0 for non-Claude window", mem)
	}
}

func TestKillWindow(t *testing.T) {
	fake := &iexec.FakeRunner{}
	err := KillWindow(fake, "project", "eng1")
	if err != nil {
		t.Fatalf("KillWindow: %v", err)
	}
	if !strings.Contains(fake.Calls[0], "tmux kill-window -t project:eng1") {
		t.Errorf("unexpected call: %s", fake.Calls[0])
	}
}

func TestKillWindow_Error(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("no such window")}
	err := KillWindow(fake, "project", "noexist")
	if err == nil {
		t.Error("expected error")
	}
}

func TestNewWindow(t *testing.T) {
	fake := &iexec.FakeRunner{}
	err := NewWindow(fake, "project", "sec")
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if !strings.Contains(fake.Calls[0], "tmux new-window -t project -n sec") {
		t.Errorf("unexpected call: %s", fake.Calls[0])
	}
}

func TestSendKeys(t *testing.T) {
	fake := &iexec.FakeRunner{}
	err := SendKeys(fake, "project:eng1", "hello world")
	if err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls (send-keys + Enter), got %d", len(fake.Calls))
	}
	if !strings.Contains(fake.Calls[0], "-l") || !strings.Contains(fake.Calls[0], "hello world") {
		t.Errorf("first call should send literal text: %s", fake.Calls[0])
	}
	if !strings.Contains(fake.Calls[1], "Enter") {
		t.Errorf("second call should send Enter: %s", fake.Calls[1])
	}
}

// fakeMultiRunner returns different output for sequential calls.
type fakeMultiRunner struct {
	callIdx   int
	responses []struct {
		output string
		err    error
	}
}

func (f *fakeMultiRunner) Run(name string, args ...string) (string, error) {
	return f.RunInDir("", name, args...)
}

func (f *fakeMultiRunner) RunInDir(dir, name string, args ...string) (string, error) {
	if f.callIdx >= len(f.responses) {
		return "", errors.New("no more responses")
	}
	resp := f.responses[f.callIdx]
	f.callIdx++
	return resp.output, resp.err
}
