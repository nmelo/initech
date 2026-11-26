package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestResolveRunningTintColor_AbsentUsesDefault: no config value -> subtler default, no warning.
func TestResolveRunningTintColor_AbsentUsesDefault(t *testing.T) {
	c, warn := resolveRunningTintColor("")
	if c != defaultRunningTintColor {
		t.Errorf("empty value => %v, want default %v", c, defaultRunningTintColor)
	}
	if warn != "" {
		t.Errorf("empty value should not warn, got %q", warn)
	}
}

// TestResolveRunningTintColor_DefaultIsTheFleetGrey pins the shipped default
// at the grey the trained fleets use, #1e2022 (ini-zjdo): a rig with no
// running_pane_tint gets it, and a running pane's default-background cell is
// drawn in it.
func TestResolveRunningTintColor_DefaultIsTheFleetGrey(t *testing.T) {
	want := tcell.NewRGBColor(0x1e, 0x20, 0x22)
	c, warn := resolveRunningTintColor("")
	if c != want || defaultRunningTintColor != want || warn != "" {
		t.Errorf("unset tint = %v (default %v, warn %q), want #1e2022", c, defaultRunningTintColor, warn)
	}
	if _, bg, _ := tintStyle(tcell.StyleDefault, c).Decompose(); bg.Hex() != 0x1e2022 {
		t.Errorf("a running pane's cell bg = #%06x, want #1e2022", bg.Hex())
	}
}

// TestResolveRunningTintColor_ASetTintIsKept: a rig's own tint is never the
// default's to replace, the old default included; none and off keep no tint.
func TestResolveRunningTintColor_ASetTintIsKept(t *testing.T) {
	for in, want := range map[string]tcell.Color{
		"#123456": tcell.NewRGBColor(0x12, 0x34, 0x56),
		"#0c120e": tcell.NewRGBColor(12, 18, 14), // the old default, set by hand
		"none":    tcell.ColorDefault,
		"OFF":     tcell.ColorDefault,
	} {
		if c, warn := resolveRunningTintColor(in); c != want || warn != "" {
			t.Errorf("%q: %v (warn %q), want %v", in, c, warn, want)
		}
	}
}

func TestResolveRunningTintColor_ValidHex(t *testing.T) {
	cases := map[string]tcell.Color{
		"#0c120e": tcell.NewRGBColor(12, 18, 14),
		"#1A0F0F": tcell.NewRGBColor(26, 15, 15), // uppercase honored
		"#0000ff": tcell.NewRGBColor(0, 0, 255),  // non-green honored (it's a color, not a green knob)
	}
	for in, want := range cases {
		c, warn := resolveRunningTintColor(in)
		if c != want {
			t.Errorf("resolveRunningTintColor(%q) = %v, want %v", in, c, want)
		}
		if warn != "" {
			t.Errorf("resolveRunningTintColor(%q) warned unexpectedly: %q", in, warn)
		}
	}
}

// TestResolveRunningTintColor_InvalidFallsBackWithWarning: unparseable values
// fall back to the default and warn (never block startup). Bare "0c120e"
// (no leading #) is intentionally invalid per the spec.
func TestResolveRunningTintColor_InvalidFallsBackWithWarning(t *testing.T) {
	for _, in := range []string{"#zzz", "#12", "green", "0c120e", "#12345", "#1234567", "##0c120e"} {
		c, warn := resolveRunningTintColor(in)
		if c != defaultRunningTintColor {
			t.Errorf("resolveRunningTintColor(%q) = %v, want default %v", in, c, defaultRunningTintColor)
		}
		if warn == "" {
			t.Errorf("resolveRunningTintColor(%q) should warn on invalid input", in)
		}
	}
}

// TestResolveRunningTintColor_NoneOrOffDisables: "none"/"off" (case-insensitive)
// disable the tint by resolving to ColorDefault (tintStyle no-op -> idle look).
func TestResolveRunningTintColor_NoneOrOffDisables(t *testing.T) {
	for _, in := range []string{"none", "off", "NONE", "Off", "  none  "} {
		c, warn := resolveRunningTintColor(in)
		if c != tcell.ColorDefault {
			t.Errorf("resolveRunningTintColor(%q) = %v, want ColorDefault (disabled)", in, c)
		}
		if warn != "" {
			t.Errorf("resolveRunningTintColor(%q) should not warn (valid disable)", in)
		}
	}
}

// TestBackgroundTint_DisabledReturnsNeutral: when the resolved tint is
// ColorDefault (none/off), a running pane still shows no tint.
func TestBackgroundTint_DisabledReturnsNeutral(t *testing.T) {
	orig := runningTintColor
	runningTintColor = tcell.ColorDefault // simulate none/off resolution
	defer func() { runningTintColor = orig }()

	p := &Pane{alive: true}
	p.lastOutputTime = time.Now()
	p.updateActivity() // StateRunning + tint hold bumped
	if got := p.backgroundTint(); got != tcell.ColorDefault {
		t.Errorf("disabled tint: backgroundTint = %v, want ColorDefault even when running", got)
	}
}
