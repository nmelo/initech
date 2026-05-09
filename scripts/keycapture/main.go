// Standalone key-capture helper for ini-4pk Phase 0 empirical verification.
//
// Run: go run ./scripts/keycapture/
// Press: Enter, Shift+Enter, Ctrl+Enter, Alt+Enter, Shift+Arrow, Ctrl+Arrow, etc.
// Quit: Esc, or Ctrl+Q.
//
// Writes one line per event to stdout AND to a file at
// .tmp/keycapture-<pid>.log so the run can be inspected afterwards
// without copy/paste.
//
// What it reveals: what tcell.EventKey actually produces in your terminal
// for each key combo, with the outer terminal in its default mode (no
// kitty/modifyOtherKeys negotiation by initech). The headline question
// is whether Shift+Enter arrives with Modifiers&ModShift set, or as a
// plain Enter event (mod=0). The answer determines whether ini-4pk Phase 1
// needs to negotiate keyboard enhancement at all.
//
// The tool uses tcell directly, so the parsing is identical to what
// initech's TUI does. Running this in the same terminal type as your
// usual initech session gives a representative answer.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
)

func main() {
	outFile := filepath.Join(".tmp", fmt.Sprintf("keycapture-%d.log", os.Getpid()))
	if err := os.MkdirAll(filepath.Dir(outFile), 0o755); err != nil {
		log.Fatalf("mkdir .tmp: %v", err)
	}
	f, err := os.Create(outFile)
	if err != nil {
		log.Fatalf("create %s: %v", outFile, err)
	}
	defer f.Close()

	screen, err := tcell.NewScreen()
	if err != nil {
		log.Fatalf("tcell.NewScreen: %v", err)
	}
	if err := screen.Init(); err != nil {
		log.Fatalf("screen.Init: %v", err)
	}
	defer screen.Fini()

	w, h := screen.Size()
	header := []string{
		"ini-4pk Phase 0 — keycapture",
		fmt.Sprintf("Terminal: %s   Size: %dx%d", os.Getenv("TERM_PROGRAM"), w, h),
		fmt.Sprintf("Logging events to: %s", outFile),
		"",
		"Press: Enter, Shift+Enter, Ctrl+Enter, Alt+Enter, Shift+Arrow, Ctrl+Arrow, plain letters",
		"Quit: Esc or Ctrl+Q",
		"",
	}
	for y, line := range header {
		drawLine(screen, 0, y, line)
	}
	screen.Show()

	row := len(header)
	count := 0
	for {
		ev := screen.PollEvent()
		switch ev := ev.(type) {
		case *tcell.EventKey:
			line := formatKey(ev)
			fmt.Fprintln(f, line)
			f.Sync()
			drawLine(screen, 0, row, fmt.Sprintf("[%3d] %s", count, line))
			row++
			count++
			if row >= h-1 {
				// Scroll: clear screen below header and reset row.
				for y := len(header); y < h; y++ {
					drawLine(screen, 0, y, strings.Repeat(" ", w))
				}
				row = len(header)
			}
			screen.Show()

			if ev.Key() == tcell.KeyEscape || (ev.Key() == tcell.KeyCtrlQ) {
				return
			}
		case *tcell.EventResize:
			screen.Sync()
		}
	}
}

func formatKey(ev *tcell.EventKey) string {
	mods := []string{}
	if ev.Modifiers()&tcell.ModShift != 0 {
		mods = append(mods, "Shift")
	}
	if ev.Modifiers()&tcell.ModCtrl != 0 {
		mods = append(mods, "Ctrl")
	}
	if ev.Modifiers()&tcell.ModAlt != 0 {
		mods = append(mods, "Alt")
	}
	if ev.Modifiers()&tcell.ModMeta != 0 {
		mods = append(mods, "Meta")
	}
	modStr := "(none)"
	if len(mods) > 0 {
		modStr = strings.Join(mods, "+")
	}
	return fmt.Sprintf("Name=%-20s Key=%d Rune=%q Mod=%s",
		ev.Name(), ev.Key(), ev.Rune(), modStr)
}

func drawLine(s tcell.Screen, x, y int, line string) {
	w, _ := s.Size()
	for i := 0; i < w; i++ {
		var r rune = ' '
		if i < len(line) {
			r = rune(line[i])
		}
		s.SetContent(x+i, y, r, nil, tcell.StyleDefault)
	}
}
