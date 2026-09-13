package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	iexec "github.com/nmelo/initech/internal/exec"
)

// AuthorityIdentity identifies the actual process serving a window handshake.
// StartedAt is captured once per process, never inferred from a shared PID file.
type AuthorityIdentity struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

var processAuthority = AuthorityIdentity{PID: os.Getpid(), StartedAt: time.Now().UTC()}

// PortHolder describes an OS-observed listening process, independent of PID files.
type PortHolder struct {
	PID       int       `json:"pid"`
	Name      string    `json:"name"`
	StartedAt time.Time `json:"started_at"`
}

// WindowPortStatus is the local main's bind result, served alongside IPC list.
// Failed means this main is not serving viewers even if another process is.
type WindowPortStatus struct {
	State   string            `json:"state"` // listening or failed; absent means disabled
	Address string            `json:"address"`
	Reason  string            `json:"reason,omitempty"`
	Main    AuthorityIdentity `json:"main"`
	Holder  *PortHolder       `json:"holder,omitempty"`
}

func processAge(start, now time.Time) string {
	if start.IsZero() {
		return "age unknown"
	}
	d := now.Sub(start).Truncate(time.Second)
	if d < 0 {
		d = 0
	}
	return "age " + d.String()
}

func (h *PortHolder) isInitech() bool {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(h.Name, `\`, "/")))
	return name == "initech" || name == "initech.exe"
}

// Summary describes the bound port or failure and a manual recovery action.
// It never offers to terminate an unidentified or foreign process.
func (s WindowPortStatus) Summary(now time.Time) string {
	if s.State == "listening" {
		return fmt.Sprintf("Window port: listening on %s; main PID %d, started %s", s.Address, s.Main.PID, s.Main.StartedAt.Format(time.RFC3339))
	}
	if s.State != "failed" {
		return "Window port: disabled"
	}
	holder := "holder unknown"
	remedy := "Identify the listener and free the port or change window_listen, then restart this main."
	if s.Holder != nil {
		holder = fmt.Sprintf("holder %s PID %d (%s)", s.Holder.Name, s.Holder.PID, processAge(s.Holder.StartedAt, now))
		if s.Holder.isInitech() {
			remedy = fmt.Sprintf("Close the older main or run kill %d after checking it, then restart this main.", s.Holder.PID)
		}
	}
	return fmt.Sprintf("Window port: FAILED on %s: %s; %s. This main serves no viewers. %s", s.Address, s.Reason, holder, remedy)
}

// WindowPortSnapshot returns the main-loop-owned bind result to IPC callers.
// Nil keeps unconfigured single-window sessions and older clients unchanged.
func (t *TUI) WindowPortSnapshot() *WindowPortStatus {
	var out *WindowPortStatus
	t.runOnMain(func() {
		if t.windowPort.State != "" {
			copy := t.windowPort
			out = &copy
		}
	})
	return out
}

func (t *TUI) recordWindowBind(address string, err error) {
	t.windowPort = WindowPortStatus{State: "listening", Address: address, Main: processAuthority}
	if err == nil {
		return
	}
	t.windowPort.State, t.windowPort.Reason = "failed", err.Error()
	inspect := t.inspectPortHolder
	if inspect == nil {
		inspect = func(addr string) *PortHolder { return inspectListenerHolder(&iexec.DefaultRunner{}, addr, time.Now()) }
	}
	t.windowPort.Holder = inspect(address)
	LogWarn("window-server", "bind failed", "addr", address, "err", err,
		"main_pid", processAuthority.PID, "holder", t.windowPort.Holder,
		"notice", t.windowPort.Summary(time.Now()))
}

func (t *TUI) applyWindowAuthority(peer string, identity *AuthorityIdentity) {
	if t.isFleetAuthority() || peer != WindowOnePeerName {
		return
	}
	t.viewerConnected = true
	t.viewerAuthority = identity
}

func (t *TUI) viewerAuthorityText() string {
	if !t.viewerConnected {
		return "main: disconnected — reconnecting"
	}
	a := t.viewerAuthority
	if a == nil || a.PID <= 0 || a.StartedAt.IsZero() {
		return "main: identity unavailable (older server)"
	}
	return fmt.Sprintf("main PID %d | started %s | %s", a.PID, a.StartedAt.Format(time.RFC3339), processAge(a.StartedAt, time.Now()))
}

// identityLineVisible reports whether this window draws the identity line: a
// viewer that has it toggled on. Window 1 never draws it.
func (t *TUI) identityLineVisible() bool {
	return !t.isFleetAuthority() && t.identityLineShown
}

// toggleIdentityLine is Option+w (ini-evdn). In a viewer it flips the line
// for this window's session. The main window has no line to show, so it says
// so on the footer instead of doing nothing (ini-162m), and changes no state.
func (t *TUI) toggleIdentityLine() {
	if t.isFleetAuthority() {
		t.cmd.error = "identity line is for viewer windows"
		return
	}
	t.identityLineShown = !t.identityLineShown
}

// renderWindowConnectionStatus runs after transient overlays so a failed bind
// stays visible for the entire session. A viewer showing its identity line
// (ini-evdn) draws it in the layout's spacer row; hidden, the row stays the
// plain spacer every window has.
func (t *TUI) renderWindowConnectionStatus() {
	s := t.screen
	w, h := s.Size()
	if w < 1 || h < 1 {
		return
	}
	if t.identityLineVisible() {
		style := tcell.StyleDefault.Background(tcell.NewRGBColor(30, 30, 30)).Foreground(tcell.ColorYellow)
		y := h - 2
		if y < 0 {
			y = 0
		}
		for x := 0; x < w; x++ {
			s.SetContent(x, y, ' ', nil, style)
		}
		for x, ch := range []rune(t.viewerAuthorityText()) {
			if x >= w {
				break
			}
			s.SetContent(x, y, ch, nil, style)
		}
	}
	if t.windowPort.State != "failed" {
		return
	}
	// A configured port with no secondary assignments still needs a notice,
	// but amber distinguishes that case from an interrupted monitor fleet.
	background := tcell.NewRGBColor(100, 70, 10)
	if t.assignment != nil {
		for _, window := range t.assignment.groupWindow {
			if window != WindowOne {
				background = tcell.NewRGBColor(125, 25, 25)
				break
			}
		}
	}
	style := tcell.StyleDefault.Background(background).Foreground(tcell.ColorWhite).Bold(true)
	lines := []string{"WINDOW PORT UNAVAILABLE", t.windowPort.Summary(time.Now())}
	y := 0
	for _, line := range lines {
		runes := []rune(line)
		for len(runes) > 0 && y < h-1 {
			n := min(w, len(runes))
			for x := 0; x < w; x++ {
				ch := ' '
				if x < n {
					ch = runes[x]
				}
				s.SetContent(x, y, ch, nil, style)
			}
			runes = runes[n:]
			y++
		}
	}
}
