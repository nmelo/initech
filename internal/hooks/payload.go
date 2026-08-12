package hooks

import (
	"encoding/json"
	"io"
)

// NotificationPayload is Claude Code's Notification hook stdin JSON, as
// MEASURED at Claude Code 2.1.229 (eng1's capture on ini-2x8, 4/4 runs):
//
//	{"session_id":..,"transcript_path":..,"cwd":"/…","prompt_id":..,
//	 "hook_event_name":"Notification","message":"Claude needs your permission",
//	 "notification_type":"permission_prompt"}
//
// Only the fields initech reads are modeled; unknown fields are ignored, so a
// Claude upgrade that ADDS fields cannot break parsing.
type NotificationPayload struct {
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
	CWD              string `json:"cwd"`
	SessionID        string `json:"session_id"`
}

// Notification type values, measured rather than guessed.
const (
	// TypePermissionPrompt is emitted for BOTH a permission prompt and an
	// AskUserQuestion -- the hook does not distinguish a question from an
	// approval, so row preview text must come from elsewhere.
	TypePermissionPrompt = "permission_prompt"

	// TypeIdlePrompt is the 60-second idle nudge ("Claude is waiting for your
	// input"). It is NOT a dialog.
	TypeIdlePrompt = "idle_prompt"
)

// CountsAsWaiting reports whether a payload means an agent is genuinely
// blocked on the operator.
//
// The idle_prompt exclusion is chime integrity, not a nicety: that event fires
// for EVERY idle agent after 60 seconds, so counting it would put the entire
// resting fleet on the needs-input list and chime for all of it -- turning the
// feature that exists to surface real questions into the loudest possible
// source of false ones. The operator calls a false chime a defect.
//
// Unknown types return false. A future Claude version introducing a new
// notification_type must not be silently promoted to "waiting" by a default
// that assumes anything unrecognized is a dialog; a missed chime is recoverable
// (OSC 777 is tier-1 and always on), a false one is a defect.
func (p NotificationPayload) CountsAsWaiting() bool {
	return p.NotificationType == TypePermissionPrompt
}

// ParseNotification decodes a Notification payload from a reader.
func ParseNotification(r io.Reader) (NotificationPayload, error) {
	var p NotificationPayload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return NotificationPayload{}, err
	}
	return p, nil
}
