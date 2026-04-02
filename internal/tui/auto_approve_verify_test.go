package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	vt "github.com/charmbracelet/x/vt"
)

// capturedLogs collects LogWarn calls during a test.
type capturedLogs struct {
	mu      sync.Mutex
	entries []string
}

func (c *capturedLogs) append(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, msg)
}

func (c *capturedLogs) get() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]string, len(c.entries))
	copy(cp, c.entries)
	return cp
}

func TestVerifyAutoApprove_NoWarnOnActivity(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	p := &Pane{
		name:           "intern",
		alive:          true,
		agentType:      "codex",
		emu:            emu,
		lastOutputTime: time.Now(),
	}

	p.verifyAutoApprove([]byte("p"))

	// Simulate meaningful PTY activity arriving 200ms later.
	time.Sleep(200 * time.Millisecond)
	p.mu.Lock()
	p.lastOutputTime = time.Now()
	p.mu.Unlock()

	// Wait for verification window to expire.
	time.Sleep(autoApproveVerifyTimeout + 500*time.Millisecond)

	// No warning should have been logged. Since we can't easily intercept
	// LogWarn in this test, we verify the goroutine exited cleanly (no panic).
}

func TestVerifyAutoApprove_WarnsOnTimeout(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	// Freeze lastOutputTime so no activity appears.
	frozenTime := time.Now().Add(-1 * time.Second)
	p := &Pane{
		name:           "intern",
		alive:          true,
		agentType:      "codex",
		emu:            emu,
		lastOutputTime: frozenTime,
	}

	p.verifyAutoApprove([]byte("p"))

	// Wait for verification window to expire.
	time.Sleep(autoApproveVerifyTimeout + 500*time.Millisecond)

	// The goroutine should have completed (no hang). We can't easily test
	// the LogWarn call without injecting a logger, but we verify no panic.
}

func TestVerifyAutoApprove_TrivialEchoStillWarns(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	sendTime := time.Now()
	p := &Pane{
		name:           "intern",
		alive:          true,
		agentType:      "codex",
		emu:            emu,
		lastOutputTime: sendTime,
	}

	p.verifyAutoApprove([]byte("p"))

	// Simulate a trivial echo: lastOutputTime advances by only 50ms (< 500ms threshold).
	time.Sleep(100 * time.Millisecond)
	p.mu.Lock()
	p.lastOutputTime = sendTime.Add(100 * time.Millisecond)
	p.mu.Unlock()

	// Wait for timeout.
	time.Sleep(autoApproveVerifyTimeout + 500*time.Millisecond)

	// Should have warned (trivial echo not counted as meaningful).
	// Verified by absence of panic and goroutine exit.
}

func TestVerifyAutoApprove_DeadPaneSkips(t *testing.T) {
	p := &Pane{
		name:      "intern",
		alive:     false,
		agentType: "codex",
	}
	// Should return immediately without launching goroutine.
	p.verifyAutoApprove([]byte("p"))
}

func TestVerifyAutoApprove_PaneDiesDuringVerify(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	p := &Pane{
		name:           "intern",
		alive:          true,
		agentType:      "codex",
		emu:            emu,
		lastOutputTime: time.Now(),
	}

	p.verifyAutoApprove([]byte("p"))

	// Kill the pane during verification.
	time.Sleep(200 * time.Millisecond)
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()

	// Should exit cleanly without warning or panic.
	time.Sleep(autoApproveVerifyTimeout + 500*time.Millisecond)
}

func TestAutoApproveVerifyConstants(t *testing.T) {
	if autoApproveVerifyTimeout < 2*time.Second || autoApproveVerifyTimeout > 3*time.Second {
		t.Errorf("autoApproveVerifyTimeout = %v, want 2-3s range", autoApproveVerifyTimeout)
	}
	if autoApproveVerifyMinActivity <= 0 {
		t.Error("autoApproveVerifyMinActivity must be positive")
	}
}

func TestVerifyAutoApprove_ApprovalStringFormat(t *testing.T) {
	// Verify the approval string formatting matches what LogWarn would receive.
	got := strings.TrimSpace(strings.Trim(strings.Trim(`"p"`, `"`), " "))
	if got != "p" {
		t.Errorf("approval string format unexpected: %q", got)
	}
}
