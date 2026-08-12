package hooks

import (
	"strings"
	"testing"
)

// Payload fixtures are VERBATIM from eng1's capture at Claude Code 2.1.229
// (recorded on ini-2x8), not invented, so a shape change in a future Claude
// version shows up as a test that stops matching reality rather than as a
// silently wrong assumption.
const (
	capturedPermissionPrompt = `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","cwd":"/tmp/sigcap","prompt_id":"p1","hook_event_name":"Notification","message":"Claude needs your permission","notification_type":"permission_prompt"}`
	capturedIdlePrompt       = `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","cwd":"/tmp/sigcap","prompt_id":"p2","hook_event_name":"Notification","message":"Claude is waiting for your input","notification_type":"idle_prompt"}`
)

func TestParseNotification_PermissionPromptCountsAsWaiting(t *testing.T) {
	p, err := ParseNotification(strings.NewReader(capturedPermissionPrompt))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.NotificationType != TypePermissionPrompt {
		t.Errorf("type = %q, want %q", p.NotificationType, TypePermissionPrompt)
	}
	if !p.CountsAsWaiting() {
		t.Error("a permission_prompt must count as waiting -- it is the dialog this feature exists to surface")
	}
}

// TestParseNotification_IdlePromptMustNotCountAsWaiting is the hard negative
// the bead calls out, and it is chime integrity rather than a nicety: the 60s
// idle nudge fires for EVERY idle agent, so counting it would put the entire
// resting fleet on the needs-input list and chime for all of it -- converting
// the feature that surfaces real questions into the loudest source of false
// ones. The operator calls a false chime a defect.
func TestParseNotification_IdlePromptMustNotCountAsWaiting(t *testing.T) {
	p, err := ParseNotification(strings.NewReader(capturedIdlePrompt))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.NotificationType != TypeIdlePrompt {
		t.Fatalf("fixture drift: type = %q, want %q", p.NotificationType, TypeIdlePrompt)
	}
	if p.CountsAsWaiting() {
		t.Error("idle_prompt counted as waiting -- every idle agent emits this, so the whole fleet would chime at rest")
	}
}

// TestParseNotification_UnknownTypeDoesNotCountAsWaiting pins the direction a
// future Claude version fails in. A new notification_type must not be promoted
// to "waiting" by a permissive default: a missed chime is recoverable, since
// OSC 777 is tier-1 and always on, while a false chime is a defect.
func TestParseNotification_UnknownTypeDoesNotCountAsWaiting(t *testing.T) {
	p, err := ParseNotification(strings.NewReader(`{"notification_type":"some_future_thing","message":"?"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.CountsAsWaiting() {
		t.Error("an unrecognised notification_type must not count as waiting")
	}
}

func TestParseNotification_UnknownFieldsDoNotBreakParsing(t *testing.T) {
	p, err := ParseNotification(strings.NewReader(
		`{"notification_type":"permission_prompt","message":"x","brand_new_field":{"a":1}}`))
	if err != nil {
		t.Fatalf("an added field broke parsing: %v", err)
	}
	if !p.CountsAsWaiting() {
		t.Error("an added field changed the verdict")
	}
}

func TestParseNotification_MalformedIsAnError(t *testing.T) {
	if _, err := ParseNotification(strings.NewReader("{not json")); err == nil {
		t.Error("malformed payload should return an error for the caller to swallow deliberately")
	}
}
