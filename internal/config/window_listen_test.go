package config

import "testing"

// TestValidate_WindowListenNormalizesBarePortToLoopback covers the ini-9ka.2
// gate field. A bare ":port" must bind loopback, not all interfaces: secondary
// windows are local terminals on the same machine, so binding 0.0.0.0 would
// expose every agent PTY to the network for a feature that never leaves the
// host. Mirrors the existing Listen normalization.
func TestValidate_WindowListenNormalizesBarePortToLoopback(t *testing.T) {
	p := &Project{
		Name:         "proj",
		Root:         "/tmp/proj",
		Roles:        []string{"eng1"},
		WindowListen: ":7500",
	}
	if err := Validate(p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.WindowListen != "127.0.0.1:7500" {
		t.Errorf("WindowListen = %q, want 127.0.0.1:7500 (bare port must default to loopback)", p.WindowListen)
	}
}

// TestValidate_WindowListenEmptyStaysEmpty is the zero-change guard at config
// level: an unset WindowListen must remain unset after validation. If
// validation ever defaulted it to an address, every single-window fleet would
// silently start a listener and the epic's "single-window users see zero
// change" claim would break at the config layer, before any TUI code runs.
func TestValidate_WindowListenEmptyStaysEmpty(t *testing.T) {
	p := &Project{
		Name:  "proj",
		Root:  "/tmp/proj",
		Roles: []string{"eng1"},
	}
	if err := Validate(p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.WindowListen != "" {
		t.Errorf("WindowListen = %q, want empty (single-window fleets must not acquire a listener address)", p.WindowListen)
	}
}
