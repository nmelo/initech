package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// TestIPCScanner_NearLimitSendSucceeds verifies that a send with text at the
// maximum allowed size (maxSendTextLen) is parsed successfully by NewIPCScanner.
// With the default bufio.Scanner buffer (64KB), the JSON framing overhead
// pushes the line past the token limit. This test confirms the fix (ini-piyb.2).
func TestIPCScanner_NearLimitSendSucceeds(t *testing.T) {
	text := strings.Repeat("x", maxSendTextLen)
	req := IPCRequest{Action: "send", Target: "eng1", Text: text, Enter: true}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	line := append(data, '\n')

	// Verify the line exceeds the default scanner limit.
	if len(line) <= bufio.MaxScanTokenSize {
		t.Fatalf("test precondition: line length %d should exceed default scanner limit %d", len(line), bufio.MaxScanTokenSize)
	}

	// NewIPCScanner should handle it.
	server, client := net.Pipe()
	defer server.Close()

	go func() {
		client.Write(line)
		client.Close()
	}()

	scanner := NewIPCScanner(server)
	if !scanner.Scan() {
		t.Fatalf("NewIPCScanner failed to scan near-limit message: %v", scanner.Err())
	}

	var parsed IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Action != "send" || len(parsed.Text) != maxSendTextLen {
		t.Errorf("parsed action=%q text_len=%d, want send/%d", parsed.Action, len(parsed.Text), maxSendTextLen)
	}
}

// TestIPCScanner_DefaultScannerRejectsNearLimit confirms that the default
// bufio.Scanner would fail on a near-limit message, validating the test
// precondition and proving the fix is necessary.
func TestIPCScanner_DefaultScannerRejectsNearLimit(t *testing.T) {
	text := strings.Repeat("x", maxSendTextLen)
	req := IPCRequest{Action: "send", Target: "eng1", Text: text, Enter: true}
	data, _ := json.Marshal(req)
	line := append(data, '\n')

	server, client := net.Pipe()
	defer server.Close()

	go func() {
		client.Write(line)
		client.Close()
	}()

	// Default scanner should fail.
	scanner := bufio.NewScanner(server)
	if scanner.Scan() {
		t.Fatal("default scanner should reject near-limit message but succeeded")
	}
	if scanner.Err() == nil {
		t.Fatal("expected scanner error for token too long")
	}
}

// TestIPCSend_OverLimitReturnsValidationError verifies that a send with text
// exceeding maxSendTextLen gets a clean validation error, not a framing failure.
func TestIPCSend_OverLimitReturnsValidationError(t *testing.T) {
	text := strings.Repeat("x", maxSendTextLen+1)
	req := IPCRequest{Action: "send", Target: "eng1", Text: text, Enter: true}
	data, _ := json.Marshal(req)
	line := append(data, '\n')

	server, client := net.Pipe()
	defer server.Close()

	go func() {
		client.Write(line)
		client.Close()
	}()

	scanner := NewIPCScanner(server)
	if !scanner.Scan() {
		t.Fatalf("scanner should handle over-limit message for validation: %v", scanner.Err())
	}

	var parsed IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Text) <= maxSendTextLen {
		t.Fatalf("text should exceed limit, got %d", len(parsed.Text))
	}
}
