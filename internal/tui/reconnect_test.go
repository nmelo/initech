package tui

import (
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
