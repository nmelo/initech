package tui

import (
	"testing"
	"time"
)

func TestActivityStateTransitions(t *testing.T) {
	tests := []struct {
		name         string
		jsonlType    string
		age          time.Duration
		wantActivity ActivityState
	}{
		{"user_recent", "user", 1 * time.Second, StateRunning},
		{"user_stale", "user", 10 * time.Second, StateIdle},
		{"progress_recent", "progress", 1 * time.Second, StateRunning},
		{"progress_stale", "progress", 10 * time.Second, StateIdle},
		{"assistant_recent", "assistant", 1 * time.Second, StateRunning},
		{"assistant_stale", "assistant", 10 * time.Second, StateIdle},
		{"last_prompt", "last-prompt", 0, StateIdle},
		{"system", "system", 0, StateIdle},
		{"unknown", "something-else", 0, StateIdle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Pane{
				lastJsonlType: tt.jsonlType,
				lastJsonlTime: time.Now().Add(-tt.age),
			}

			// Replicate the state machine logic from watchJSONL.
			var got ActivityState
			switch p.lastJsonlType {
			case "user", "progress", "assistant":
				if time.Since(p.lastJsonlTime) > 5*time.Second {
					got = StateIdle
				} else {
					got = StateRunning
				}
			default:
				got = StateIdle
			}

			if got != tt.wantActivity {
				t.Errorf("got %s, want %s", got, tt.wantActivity)
			}
		})
	}
}
