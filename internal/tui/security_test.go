package tui

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// TestShellQuoteArgs verifies that shellQuoteArgs produces output that is safe
// from shell injection when passed to sh -c.
func TestShellQuoteArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "simple args",
			args: []string{"claude", "--model", "sonnet"},
			want: "'claude' '--model' 'sonnet'",
		},
		{
			name: "arg with spaces",
			args: []string{"claude", "--flag", "value with spaces"},
			want: "'claude' '--flag' 'value with spaces'",
		},
		{
			name: "arg with single quote injection",
			args: []string{"claude", "--flag", "value'; rm -rf /; echo '"},
			want: `'claude' '--flag' 'value'"'"'; rm -rf /; echo '"'"''`,
		},
		{
			name: "arg with dollar sign",
			args: []string{"claude", "$(evil)"},
			want: "'claude' '$(evil)'",
		},
		{
			name: "arg with backtick",
			args: []string{"claude", "`evil`"},
			want: "'claude' '`evil`'",
		},
		{
			name: "empty args list",
			args: []string{},
			want: "",
		},
		{
			name: "single arg",
			args: []string{"claude"},
			want: "'claude'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shellQuoteArgs(tc.args)
			if got != tc.want {
				t.Errorf("shellQuoteArgs(%v):\n  got  %q\n  want %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestShellQuoteArgs_EscapeSequencePresent verifies that single quotes in args
// are properly escaped with the '"'"' sequence so they can't break out of
// single-quote quoting.
func TestShellQuoteArgs_EscapeSequencePresent(t *testing.T) {
	// A crafted input that would escape a naive quoting scheme.
	malicious := []string{"prog", "'; echo INJECTED; echo '"}
	result := shellQuoteArgs(malicious)

	// Every single quote in the original arg must be escaped as '"'"'
	// so that the shell cannot interpret it as an argument boundary.
	if !strings.Contains(result, `'"'"'`) {
		t.Errorf("expected single-quote escape sequence in output: %q", result)
	}
	// The result must still preserve the original content as literal text.
	if !strings.Contains(result, "INJECTED") {
		t.Errorf("original content missing from quoted output: %q", result)
	}
}

// TestHandleIPCBead_DELRejected verifies that bead IDs containing DEL (0x7F)
// are rejected. The original check was ch < 0x20 which missed DEL.
func TestHandleIPCBead_DELRejected(t *testing.T) {
	quitCh := make(chan struct{})
	tui := &TUI{
		quitCh: quitCh,
		ipcCh:  nil, // runOnMain executes inline
		panes:  toPaneViews([]*Pane{{name: "eng1"}}),
	}

	c1, c2 := net.Pipe()
	defer c1.Close()

	// Read the response from c2 in a goroutine.
	respCh := make(chan IPCResponse, 1)
	go func() {
		dec := json.NewDecoder(c2)
		var resp IPCResponse
		_ = dec.Decode(&resp)
		respCh <- resp
		c2.Close()
	}()

	req := IPCRequest{
		Target: "eng1",
		Text:   "ini-abc\x7f1", // contains DEL
	}
	tui.handleIPCBead(c1, req)
	c1.Close()

	resp := <-respCh
	if resp.OK {
		t.Error("expected error response for bead ID containing DEL, got OK")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message for DEL in bead ID")
	}
}

// TestHandleIPCBead_ControlCharRejected verifies that bead IDs containing
// control characters below 0x20 are also rejected (existing behaviour).
func TestHandleIPCBead_ControlCharRejected(t *testing.T) {
	quitCh := make(chan struct{})
	tui := &TUI{
		quitCh: quitCh,
		ipcCh:  nil,
		panes:  toPaneViews([]*Pane{{name: "eng1"}}),
	}

	c1, c2 := net.Pipe()
	defer c1.Close()

	respCh := make(chan IPCResponse, 1)
	go func() {
		dec := json.NewDecoder(c2)
		var resp IPCResponse
		_ = dec.Decode(&resp)
		respCh <- resp
		c2.Close()
	}()

	req := IPCRequest{
		Target: "eng1",
		Text:   "ini-abc\x01", // contains SOH (0x01)
	}
	tui.handleIPCBead(c1, req)
	c1.Close()

	resp := <-respCh
	if resp.OK {
		t.Error("expected error response for bead ID containing control char, got OK")
	}
}

// TestSocketPath_Security verifies the socket is placed in .initech/ when projectRoot is set.
func TestSocketPath_Security(t *testing.T) {
	t.Run("with root", func(t *testing.T) {
		got := SocketPath("/home/user/project", "myproject")
		want := "/home/user/project/.initech/initech.sock"
		if got != want {
			t.Errorf("SocketPath with root: got %q, want %q", got, want)
		}
	})

	t.Run("empty root falls back to tmp", func(t *testing.T) {
		got := SocketPath("", "myproject")
		if !strings.HasPrefix(got, "/tmp/") {
			t.Errorf("SocketPath empty root: expected /tmp/ prefix, got %q", got)
		}
		if !strings.Contains(got, "myproject") {
			t.Errorf("SocketPath empty root: expected project name in path, got %q", got)
		}
	})
}
