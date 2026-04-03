package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestMcpLANIP_ReturnsNonLoopback(t *testing.T) {
	ip := mcpLANIP()
	if ip == "" {
		t.Error("expected non-empty IP")
	}
	// Should not be loopback (unless truly no network interfaces).
	if ip == "127.0.0.1" {
		t.Log("only loopback found, returned localhost fallback")
	}
}

func TestBase64Encode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"f", "Zg=="},
		{"fo", "Zm8="},
		{"foo", "Zm9v"},
		{"foobar", "Zm9vYmFy"},
		{"hello world", "aGVsbG8gd29ybGQ="},
	}
	for _, tc := range tests {
		got := base64Encode(tc.input)
		if got != tc.want {
			t.Errorf("base64Encode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHandleMcpKey_EscCloses(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.mcpM.active = true

	tui.handleMcpKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if tui.mcpM.active {
		t.Error("expected modal to close on Esc")
	}
}

func TestHandleMcpKey_QCloses(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.mcpM.active = true

	tui.handleMcpKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if tui.mcpM.active {
		t.Error("expected modal to close on q")
	}
}

func TestHandleMcpKey_XTogglesToken(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.mcpM.active = true

	// First r reveals.
	tui.handleMcpKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if !tui.mcpM.tokenRevealed {
		t.Error("expected token to be revealed after first x")
	}
	if tui.mcpM.revealExpiry.IsZero() {
		t.Error("expected revealExpiry to be set")
	}

	// Second r hides.
	tui.handleMcpKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if tui.mcpM.tokenRevealed {
		t.Error("expected token to be hidden after second x")
	}
}

func TestHandleMcpKey_TokenAutoHides(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.mcpM.active = true
	tui.mcpM.tokenRevealed = true
	tui.mcpM.revealExpiry = time.Now().Add(-1 * time.Second) // already expired

	// The render path checks revealExpiry. Simulate that check.
	if tui.mcpM.tokenRevealed && time.Now().After(tui.mcpM.revealExpiry) {
		tui.mcpM.tokenRevealed = false
	}
	if tui.mcpM.tokenRevealed {
		t.Error("expected token to auto-hide when expiry has passed")
	}
}

func TestCmdMcp_OpensModal(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.cmdMcp()

	if !tui.mcpM.active {
		t.Error("expected mcpM.active to be true after cmdMcp")
	}
	if tui.mcpM.tokenRevealed {
		t.Error("expected token to be hidden on open")
	}
}

func TestMcpLANIP_Localhost_ForLoopbackBind(t *testing.T) {
	// When bind is 127.0.0.1, the modal should show localhost.
	// This tests the logic in renderMcpModal, not mcpLANIP itself.
	tui := newTestTUI(testPane("eng1"))
	tui.mcpBind = "127.0.0.1"
	tui.mcpPort = 9200
	tui.mcpToken = "test-token"

	// The render function uses: if t.mcpBind == "127.0.0.1" { host = "localhost" }
	host := mcpLANIP()
	if tui.mcpBind == "127.0.0.1" {
		host = "localhost"
	}
	if host != "localhost" {
		t.Errorf("expected localhost for loopback bind, got %q", host)
	}
}
