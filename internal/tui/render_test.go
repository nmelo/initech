// render_test.go tests render helpers.
package tui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestRenderCmdError_NarrowTerminal verifies renderCmdError does not panic
// when the terminal is too narrow (sw < 5). Previously msg[:sw-4] would
// cause a slice-bounds panic when sw <= 4 (ini-a1e.6).
func TestRenderCmdError_NarrowTerminal(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			s := tcell.NewSimulationScreen("")
			s.Init()
			s.SetSize(width, 10)
			tui := &TUI{
				screen: s,
				cmd:    cmdModal{error: "something went wrong"},
			}
			// Must not panic.
			tui.renderCmdError()
		})
	}
}

// TestRenderCmdError_NormalWidth verifies renderCmdError renders without panic
// for a standard terminal width.
func TestRenderCmdError_NormalWidth(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		cmd:    cmdModal{error: "build failed"},
	}
	// Must not panic.
	tui.renderCmdError()
}
