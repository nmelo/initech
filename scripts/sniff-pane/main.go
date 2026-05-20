// Standalone byte-stream classifier for TUI agents — ini-szir Phase 1A artifact.
//
// Spawns a child process under a PTY, captures its stdout for N seconds, and
// classifies the byte stream into prose / CSI sequences / box-drawing chars /
// absolute-positioning sequences. Used to answer the "does this agent's
// output reflow safely under a width-lie?" question that came out of
// ini-szir round 3.
//
// Usage:
//   go run ./scripts/sniff-pane/ -- <command> [args...]
//   go run ./scripts/sniff-pane/ -seconds 20 -cols 20 -- claude
//
// Default: 20 columns wide, 15 seconds, no command (you must pass --).
//
// Headline metric for ini-szir-style investigations: absolute-positioning
// sequences (CUP/CHA/VPA). Non-zero means the agent renders with absolute
// column references and would mis-render under a PTY-width lie.
//
// NOT shipped as part of initech; lives under scripts/ as a debug tool for
// future investigations of the same shape (similar to scripts/keycapture/
// from ini-4pk Phase 0).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
)

type stats struct {
	total, printable, newline, ctrl                        atomic.Uint64
	csiTotal, csiCUP, csiCHA, csiVPA, csiSGR, csiED, csiEL atomic.Uint64
	csiCursorRel, csiMode, csiOther                        atomic.Uint64
	osc, escOther, boxDraw                                 atomic.Uint64
}

var s stats

func classify(data []byte) {
	if len(data) == 0 {
		return
	}
	s.total.Add(uint64(len(data)))
	i := 0
	for i < len(data) {
		b := data[i]
		switch {
		case b == 0x1b:
			if i+1 >= len(data) {
				s.escOther.Add(1)
				i++
				continue
			}
			next := data[i+1]
			switch next {
			case '[':
				j := i + 2
				for j < len(data) && data[j] >= 0x30 && data[j] <= 0x3f {
					j++
				}
				for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
					j++
				}
				if j < len(data) {
					switch data[j] {
					case 'H', 'f':
						s.csiCUP.Add(1)
					case 'G':
						s.csiCHA.Add(1)
					case 'd':
						s.csiVPA.Add(1)
					case 'm':
						s.csiSGR.Add(1)
					case 'J':
						s.csiED.Add(1)
					case 'K':
						s.csiEL.Add(1)
					case 'A', 'B', 'C', 'D':
						s.csiCursorRel.Add(1)
					case 'h', 'l':
						s.csiMode.Add(1)
					default:
						s.csiOther.Add(1)
					}
					i = j + 1
				} else {
					i = len(data)
				}
				s.csiTotal.Add(1)
			case ']':
				j := i + 2
				for j < len(data) {
					if data[j] == 0x07 {
						j++
						break
					}
					if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				s.osc.Add(1)
				i = j
			default:
				s.escOther.Add(1)
				i += 2
			}
		case b == '\r' || b == '\n':
			s.newline.Add(1)
			i++
		case b >= 0x20 && b <= 0x7e:
			s.printable.Add(1)
			i++
		case b >= 0x80:
			if b == 0xe2 && i+2 < len(data) {
				b1 := data[i+1]
				b2 := data[i+2]
				if (b1 == 0x94 && b2 >= 0x80) || (b1 == 0x95 && b2 <= 0xbf) {
					s.boxDraw.Add(1)
				}
			}
			if b < 0xc0 {
				i++
			} else if b < 0xe0 {
				i += 2
			} else if b < 0xf0 {
				i += 3
			} else {
				i += 4
			}
		default:
			s.ctrl.Add(1)
			i++
		}
	}
}

func main() {
	var (
		seconds int
		cols    int
		rows    int
		prompt  string
	)
	flag.IntVar(&seconds, "seconds", 15, "capture duration after the child is launched")
	flag.IntVar(&cols, "cols", 20, "PTY column count to advertise to the child")
	flag.IntVar(&rows, "rows", 24, "PTY row count to advertise to the child")
	flag.StringVar(&prompt, "prompt", "", "text to type into the child after launch (with CR appended)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sniff-pane [-seconds N] [-cols N] [-rows N] [-prompt TEXT] -- <command> [args...]")
		os.Exit(2)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("COLUMNS=%d", cols),
		fmt.Sprintf("LINES=%d", rows),
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pty.Start:", err)
		os.Exit(1)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		fmt.Fprintln(os.Stderr, "pty.Setsize:", err)
	}

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				classify(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	if prompt != "" {
		// Wait for the child to settle before sending input.
		time.Sleep(3 * time.Second)
		_, _ = io.WriteString(ptmx, prompt+"\r")
	}
	time.Sleep(time.Duration(seconds) * time.Second)

	report()
}

func report() {
	total := s.total.Load()
	if total == 0 {
		fmt.Println("(no bytes captured)")
		return
	}
	pct := func(n uint64) string { return fmt.Sprintf("%5.2f%%", float64(n)/float64(total)*100) }

	fmt.Printf("Total bytes: %d\n\n", total)
	fmt.Printf("Categories:\n")
	fmt.Printf("  printable (prose):   %12d  %s\n", s.printable.Load(), pct(s.printable.Load()))
	fmt.Printf("  newlines (\\r\\n):     %12d  %s\n", s.newline.Load(), pct(s.newline.Load()))
	fmt.Printf("  other C0 ctrl:       %12d  %s\n", s.ctrl.Load(), pct(s.ctrl.Load()))
	fmt.Printf("  CSI total:           %12d\n", s.csiTotal.Load())
	fmt.Printf("  OSC:                 %12d\n", s.osc.Load())
	fmt.Printf("  other ESC:           %12d\n", s.escOther.Load())
	fmt.Printf("  box-drawing chars:   %12d  %s\n", s.boxDraw.Load(), pct(s.boxDraw.Load()))

	fmt.Printf("\nCSI breakdown (Option-K-style decision data):\n")
	fmt.Printf("  CUP (H/f) ABSOLUTE:  %12d   *** width-lie red flag ***\n", s.csiCUP.Load())
	fmt.Printf("  CHA (G)   ABSOLUTE:  %12d   *** width-lie red flag ***\n", s.csiCHA.Load())
	fmt.Printf("  VPA (d):             %12d\n", s.csiVPA.Load())
	fmt.Printf("  SGR (m):             %12d\n", s.csiSGR.Load())
	fmt.Printf("  relative cursor:     %12d\n", s.csiCursorRel.Load())
	fmt.Printf("  ED (J):              %12d\n", s.csiED.Load())
	fmt.Printf("  EL (K):              %12d\n", s.csiEL.Load())
	fmt.Printf("  mode set/reset:      %12d\n", s.csiMode.Load())
	fmt.Printf("  other CSI:           %12d\n", s.csiOther.Load())

	absTotal := s.csiCUP.Load() + s.csiCHA.Load() + s.csiVPA.Load()
	fmt.Printf("\nAbsolute positioning: %d sequences (%s of bytes)\n", absTotal, pct(absTotal))
	if absTotal == 0 {
		fmt.Println("-> Width-lie analyses are SAFE on this output.")
	} else if pctF := float64(absTotal) / float64(total) * 100; pctF < 0.5 {
		fmt.Printf("-> Width-lie analyses are PROBABLY SAFE (<0.5%% absolute positioning).\n")
	} else {
		fmt.Printf("-> Width-lie analyses are LIKELY UNSAFE (%.2f%% absolute positioning).\n", pctF)
	}
}
