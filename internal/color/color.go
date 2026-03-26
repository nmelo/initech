// Package color provides ANSI color helpers for CLI output.
//
// Colors are auto-disabled when NO_COLOR is set, stdout is not a TTY, or
// SetEnabled(false) has been called (via --no-color). All functions return the
// input string unchanged when colors are disabled, making them safe to use
// unconditionally.
//
// Table alignment: colored strings contain ANSI escape bytes that are not
// visible. Use Pad(s, width) instead of %-Ns to produce correctly padded
// columns from colored strings.
package color

import (
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

var enabled = true

func init() {
	// Check --no-color before cobra parses flags so that cobra parse errors
	// (printed before PersistentPreRunE runs) are also uncolored.
	if hasNoColorArg(os.Args[1:]) {
		enabled = false
		return
	}
	if os.Getenv("NO_COLOR") != "" {
		enabled = false
		return
	}
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		enabled = false
	}
}

// hasNoColorArg returns true if args contains the literal string "--no-color".
// Extracted from init() to allow unit testing without restarting the process.
func hasNoColorArg(args []string) bool {
	for _, arg := range args {
		if arg == "--no-color" {
			return true
		}
	}
	return false
}

// SetEnabled overrides the auto-detected color state. Call with false to honor
// the --no-color flag.
func SetEnabled(v bool) { enabled = v }

// Enabled reports whether colors are currently active.
func Enabled() bool { return enabled }

var (
	styleGreen  = ansi.NewStyle().ForegroundColor(ansi.BrightGreen)
	styleRed    = ansi.NewStyle().ForegroundColor(ansi.BrightRed)
	styleYellow = ansi.NewStyle().ForegroundColor(ansi.BrightYellow)
	styleBlue   = ansi.NewStyle().ForegroundColor(ansi.BrightBlue)
	styleCyan   = ansi.NewStyle().ForegroundColor(ansi.BrightCyan)
	styleDim    = ansi.NewStyle().Faint()
	styleBold   = ansi.NewStyle().Bold()
)

// Green returns s in bright green, or s unchanged if colors are disabled.
func Green(s string) string { return apply(styleGreen, s) }

// Red returns s in bright red, or s unchanged if colors are disabled.
func Red(s string) string { return apply(styleRed, s) }

// Yellow returns s in bright yellow, or s unchanged if colors are disabled.
func Yellow(s string) string { return apply(styleYellow, s) }

// Blue returns s in bright blue, or s unchanged if colors are disabled.
func Blue(s string) string { return apply(styleBlue, s) }

// Cyan returns s in bright cyan, or s unchanged if colors are disabled.
func Cyan(s string) string { return apply(styleCyan, s) }

// Dim returns s in dim/faint style, or s unchanged if colors are disabled.
func Dim(s string) string { return apply(styleDim, s) }

// Bold returns s in bold, or s unchanged if colors are disabled.
func Bold(s string) string { return apply(styleBold, s) }

// RedBold returns s in red and bold. Useful for error keywords like "Error:" or "MISSING".
func RedBold(s string) string {
	if !enabled {
		return s
	}
	return ansi.NewStyle().ForegroundColor(ansi.BrightRed).Bold().Styled(s)
}

// YellowBold returns s in yellow and bold. Useful for warning keywords.
func YellowBold(s string) string {
	if !enabled {
		return s
	}
	return ansi.NewStyle().ForegroundColor(ansi.BrightYellow).Bold().Styled(s)
}

// GreenBold returns s in green and bold. Useful for success summaries.
func GreenBold(s string) string {
	if !enabled {
		return s
	}
	return ansi.NewStyle().ForegroundColor(ansi.BrightGreen).Bold().Styled(s)
}

// BlueBold returns s in blue and bold. Useful for agent name headers.
func BlueBold(s string) string {
	if !enabled {
		return s
	}
	return ansi.NewStyle().ForegroundColor(ansi.BrightBlue).Bold().Styled(s)
}

// CyanBold returns s in cyan and bold. Useful for section headers.
func CyanBold(s string) string {
	if !enabled {
		return s
	}
	return ansi.NewStyle().ForegroundColor(ansi.BrightCyan).Bold().Styled(s)
}

// Pad pads s to at least width visible characters using spaces. Unlike %-Ns
// format verbs, Pad uses ansi.StringWidth to measure the visible width of s
// (ignoring ANSI escape sequences), so colored strings align correctly.
func Pad(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func apply(style ansi.Style, s string) string {
	if !enabled {
		return s
	}
	return style.Styled(s)
}
