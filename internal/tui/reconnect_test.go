package tui

import (
	"net"
	"testing"
	"time"
)

func TestBackoff_ExponentialWithCap(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second}, // capped
		{6, 30 * time.Second}, // stays capped
		{10, 30 * time.Second},
	}
	for _, tt := range tests {
		got := backoff(tt.attempt)
		if got != tt.want {
			t.Errorf("backoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestBackoff_NeverExceedsMax(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := backoff(i)
		if d > reconnectMax {
			t.Errorf("backoff(%d) = %v, exceeds max %v", i, d, reconnectMax)
		}
		if d < 0 {
			t.Errorf("backoff(%d) = %v, negative (overflow)", i, d)
		}
	}
}

func TestBackoff_NoOverflowAtHighIteration(t *testing.T) {
	// Iteration 34+ caused int64 overflow before the guard was added.
	for _, i := range []int{34, 50, 63, 100, 1000} {
		d := backoff(i)
		if d != reconnectMax {
			t.Errorf("backoff(%d) = %v, want %v (capped)", i, d, reconnectMax)
		}
	}
}

func TestPeerManager_QuitsCleanly(t *testing.T) {
	// Verify the manager exits when quit is closed, even with no remotes.
	quit := make(chan struct{})
	pm := &peerManager{quit: quit}
	close(quit)
	// Should not hang.
	pm.wait()
}

func TestConsumeEvents_ForwardSend(t *testing.T) {
	// Simulate a daemon pushing a forward_send command through the control
	// stream. Verify the onForwardSend callback fires with the right args.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	mux := NewControlMux(client)
	defer mux.Close()

	delivered := make(chan struct{})
	var gotTarget, gotText string
	var gotEnter bool

	quit := make(chan struct{})
	done := make(chan struct{})
	pm := &peerManager{
		quit: quit,
		onForwardSend: func(target, text string, enter bool) error {
			gotTarget = target
			gotText = text
			gotEnter = enter
			close(delivered)
			return nil
		},
	}

	go pm.consumeEvents("testpeer", mux, done)

	// Write a forward_send command to the server side (daemon writes this).
	cmd := ControlCmd{Action: "forward_send", Target: "super", Text: "hello from remote", Enter: true}
	writeJSON(server, cmd)

	select {
	case <-delivered:
		if gotTarget != "super" {
			t.Errorf("target = %q, want super", gotTarget)
		}
		if gotText != "hello from remote" {
			t.Errorf("text = %q, want 'hello from remote'", gotText)
		}
		if !gotEnter {
			t.Error("enter should be true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forward_send not delivered within 2s")
	}

	close(done)
}
