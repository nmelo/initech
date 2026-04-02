package color_test

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/color"
)

func enableColors(t *testing.T) {
	t.Helper()
	color.SetEnabled(true)
	t.Cleanup(func() { color.SetEnabled(false) })
}

func disableColors(t *testing.T) {
	t.Helper()
	color.SetEnabled(false)
}

// TestDisabledPassthrough verifies all functions return the input unchanged
// when colors are disabled.
func TestDisabledPassthrough(t *testing.T) {
	disableColors(t)
	input := "hello"
	funcs := []struct {
		name string
		fn   func(string) string
	}{
		{"Green", color.Green},
		{"Red", color.Red},
		{"Yellow", color.Yellow},
		{"Blue", color.Blue},
		{"Cyan", color.Cyan},
		{"Dim", color.Dim},
		{"Bold", color.Bold},
		{"RedBold", color.RedBold},
		{"YellowBold", color.YellowBold},
		{"GreenBold", color.GreenBold},
		{"BlueBold", color.BlueBold},
		{"CyanBold", color.CyanBold},
	}
	for _, f := range funcs {
		t.Run(f.name, func(t *testing.T) {
			if got := f.fn(input); got != input {
				t.Errorf("%s disabled: got %q, want %q", f.name, got, input)
			}
		})
	}
}

// TestEnabledContainsESC verifies that each color function wraps the string
// in ANSI escape sequences when colors are enabled.
func TestEnabledContainsESC(t *testing.T) {
	enableColors(t)
	input := "hello"
	funcs := []struct {
		name string
		fn   func(string) string
	}{
		{"Green", color.Green},
		{"Red", color.Red},
		{"Yellow", color.Yellow},
		{"Blue", color.Blue},
		{"Cyan", color.Cyan},
		{"Dim", color.Dim},
		{"Bold", color.Bold},
		{"RedBold", color.RedBold},
		{"YellowBold", color.YellowBold},
		{"GreenBold", color.GreenBold},
		{"BlueBold", color.BlueBold},
		{"CyanBold", color.CyanBold},
	}
	for _, f := range funcs {
		t.Run(f.name, func(t *testing.T) {
			got := f.fn(input)
			if got == input {
				t.Errorf("%s enabled: output unchanged, expected ANSI codes", f.name)
			}
			if !strings.Contains(got, "\x1b") {
				t.Errorf("%s enabled: output missing ESC byte", f.name)
			}
			if !strings.Contains(got, input) {
				t.Errorf("%s enabled: original text missing from output", f.name)
			}
		})
	}
}

// TestSetEnabled verifies the toggle works.
func TestSetEnabled(t *testing.T) {
	color.SetEnabled(true)
	if color.Green("x") == "x" {
		t.Fatal("Green should add ANSI codes when enabled")
	}

	color.SetEnabled(false)
	if color.Green("x") != "x" {
		t.Fatal("Green should pass through when disabled")
	}
}

// TestPadDisabled verifies Pad pads plain strings correctly.
func TestPadDisabled(t *testing.T) {
	disableColors(t)
	got := color.Pad("abc", 8)
	if len(got) != 8 {
		t.Errorf("Pad disabled: got len=%d, want 8; value=%q", len(got), got)
	}
	if got[:3] != "abc" {
		t.Errorf("Pad disabled: prefix wrong: %q", got)
	}
}

// TestPadEnabled verifies Pad measures visible width (not byte length) so
// colored strings align correctly.
func TestPadEnabled(t *testing.T) {
	enableColors(t)
	colored := color.Blue("abc") // "abc" with ANSI codes, visible width 3
	got := color.Pad(colored, 8)
	// Visible width should be exactly 8.
	// ansi.StringWidth(got) strips escape codes and counts runes.
	// We verify by checking the plain text length after stripping.
	// The simplest check: strip ANSI and measure.
	stripped := stripANSI(got)
	if len(stripped) != 8 {
		t.Errorf("Pad enabled: visible width=%d, want 8; stripped=%q", len(stripped), stripped)
	}
}

// TestPadNoPadNeeded verifies Pad returns s unchanged when already at width.
func TestPadNoPadNeeded(t *testing.T) {
	got := color.Pad("hello", 3)
	if got != "hello" {
		t.Errorf("Pad: expected unchanged string when s >= width; got %q", got)
	}
}

// stripANSI removes ANSI escape sequences for test verification.
func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until final byte (letter in range 0x40-0x7E)
			i += 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7E) {
				i++
			}
			i++ // skip the final byte
		} else {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}
