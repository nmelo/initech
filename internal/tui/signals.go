// signals.go installs OS signal handlers so every external termination leaves
// a trace in initech.log before the process exits.
//
// Without this, SIGTERM/SIGHUP/SIGKILL from the OS kill the process silently
// and leave the terminal in raw mode (screen.Fini never runs). The handlers
// here fix that for catchable signals. SIGKILL still can't be caught — for
// that case, the PID file + system log check in pid.go provides post-mortem
// evidence.
package tui

import (
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gdamore/tcell/v2"
)

// exitFunc is called to exit the process when a fatal signal is received.
// Overridable in tests to prevent actual process exit.
var exitFunc = func(code int) { os.Exit(code) }

// installSignalHandlers registers handlers for signals that terminate the
// process. Each handler logs the signal name, restores the terminal via
// screen.Fini(), and calls os.Exit(2). Returns a cleanup func that stops
// signal delivery.
//
// Covered signals:
//   - SIGTERM: sent by init/systemd, `kill <pid>`, container shutdown
//   - SIGHUP: terminal hangup (SSH disconnect, terminal closed)
//   - SIGQUIT: Ctrl+\ — like SIGINT but also dumps goroutine stacks to stderr
//   - SIGABRT: abort() from cgo code; Go runtime itself uses SIGABRT for
//     some fatal errors
//   - SIGINT: Ctrl+C or `kill -INT <pid>` sent from outside the TUI
//
// Not handled: SIGKILL (uncatchable), SIGWINCH (tcell uses it for resize).
func installSignalHandlers(screen tcell.Screen, quitCh chan struct{}) func() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch,
		syscall.SIGTERM,
		syscall.SIGHUP,
		syscall.SIGQUIT,
		syscall.SIGABRT,
		syscall.SIGINT,
	)

	// exitOnce ensures os.Exit(2) fires at most once. The cleanup function
	// claims the Once first (via a no-op Do) to disarm the goroutine: if
	// a signal arrives just as graceful shutdown begins, the goroutine's
	// Do becomes a no-op and the deferred cleanup path runs normally.
	var exitOnce sync.Once

	go func() {
		sig, ok := <-ch
		if !ok {
			return // cleanup called, channel closed
		}
		// Log before touching the screen so the entry is always written even
		// if Fini() panics (unlikely but defensive).
		LogError("tui", "killed by signal", "signal", sig.String())
		if screen != nil {
			screen.Fini() // restore terminal to cooked mode
		}
		exitOnce.Do(func() { exitFunc(2) })
	}()

	return func() {
		signal.Stop(ch)
		// Disarm: claim the Once so a concurrently-executing goroutine can't
		// call os.Exit after we return.
		exitOnce.Do(func() {})
		// Drain any signals that arrived before Stop() could suppress them.
		// signal.Stop guarantees no new signals, but buffered ones remain.
		for len(ch) > 0 {
			<-ch
		}
		close(ch)
	}
}
