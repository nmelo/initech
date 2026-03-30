// remote_pane.go implements PaneView for network-backed agent panes. A
// RemotePane connects to a headless daemon via yamux and presents the remote
// agent as a local pane in the TUI grid. PTY bytes flow downstream (daemon ->
// local emulator) for rendering, and keystrokes flow upstream (TUI -> daemon
// -> PTY) for input.
package tui

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// Compile-time assertion: RemotePane implements PaneView.
var _ PaneView = (*RemotePane)(nil)

// resizeDebounce is how long to wait after the last resize before sending
// the final dimensions to the remote daemon. Prevents SIGWINCH storms
// (from dragging the terminal edge) from flooding the control channel.
const resizeDebounce = 50 * time.Millisecond

// RemotePane is a PaneView backed by a yamux stream to a headless daemon.
// The local VT emulator receives PTY bytes from the stream for rendering.
// Keystrokes are forwarded upstream to the daemon for injection into the PTY.
type RemotePane struct {
	name   string            // Agent name (e.g. "eng1").
	host   string            // Peer name of the remote daemon (e.g. "workbench").
	stream net.Conn          // Yamux stream: downstream PTY bytes + upstream keystrokes.
	mux    *ControlMux       // Shared multiplexed control channel (thread-safe).
	emu    *vt.SafeEmulator  // Local VT emulator owned exclusively by the main goroutine.
	dataCh chan []byte        // readLoop sends byte chunks here; Render drains and writes to emu.
	mu     sync.Mutex
	alive    bool
	activity ActivityState
	lastOut  time.Time
	beadID   string
	sessDesc string
	region   Region

	goWg sync.WaitGroup // Tracks readLoop goroutine. Close waits on this.

	// Resize debounce: pendingResize holds the latest requested dimensions.
	// The timer fires after resizeDebounce and sends the final geometry.
	resizeMu      sync.Mutex
	resizeTimer   *time.Timer
	pendingRows   int
	pendingCols   int
}

// NewRemotePane creates a RemotePane connected to a remote agent.
// The mux is shared across all RemotePanes from the same peer connection.
// The caller must call Start() to begin the readLoop goroutine.
func NewRemotePane(name, host string, stream net.Conn, mux *ControlMux, cols, rows int) *RemotePane {
	return &RemotePane{
		name:     name,
		host:     host,
		stream:   stream,
		mux:      mux,
		emu:      vt.NewSafeEmulator(cols, rows),
		dataCh:   make(chan []byte, 64), // Buffered: readLoop sends, Render drains.
		alive:    true,
		activity: StateIdle,
	}
}

// Start launches background goroutines: readLoop (stream -> dataCh) and
// responseLoop (drains emulator responses so Write never blocks on the
// internal io.Pipe).
func (rp *RemotePane) Start() {
	rp.goWg.Add(2)
	go func() {
		defer rp.goWg.Done()
		rp.readLoop()
	}()
	go func() {
		defer rp.goWg.Done()
		rp.responseLoop()
	}()
}

// readLoop reads PTY output from the yamux stream and sends byte chunks to
// dataCh. The main goroutine drains dataCh in Render and writes to the
// emulator. This eliminates all mutex contention: the emulator is only ever
// accessed from the main goroutine.
func (rp *RemotePane) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := rp.stream.Read(buf)
		if n > 0 {
			// Copy and send to channel. The main goroutine's Render drains
			// this channel and writes to the emulator (zero contention).
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case rp.dataCh <- chunk:
			default:
				// Channel full: drop oldest to make room (backpressure).
				select {
				case <-rp.dataCh:
				default:
				}
				rp.dataCh <- chunk
			}
			now := time.Now()
			rp.mu.Lock()
			rp.lastOut = now
			rp.activity = StateRunning
			rp.mu.Unlock()
		}
		if err != nil {
			LogInfo("remote-readloop", "stream ended",
				"agent", rp.name, "host", rp.host, "err", err)
			rp.mu.Lock()
			rp.alive = false
			rp.activity = StateDead
			rp.mu.Unlock()
			return
		}
	}
}

// responseLoop drains the emulator's internal response pipe. The VT emulator
// writes responses (DA, DSR, cursor position reports) to an io.Pipe when it
// encounters query sequences in the byte stream. Without a reader, io.Pipe.Write
// blocks, which deadlocks Emulator.Write (and therefore SafeEmulator.Write)
// while holding the write lock. For RemotePanes the responses are discarded;
// the daemon's local emulator handles the real DA responses via its own PTY.
func (rp *RemotePane) responseLoop() {
	buf := make([]byte, 256)
	for {
		_, err := rp.emu.Read(buf)
		if err != nil {
			return
		}
	}
}

// ── PaneView interface ──────────────────────────────────────────────

func (rp *RemotePane) Name() string { return rp.name }
func (rp *RemotePane) Host() string { return rp.host }

func (rp *RemotePane) IsAlive() bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.alive
}

func (rp *RemotePane) IsSuspended() bool { return false }
func (rp *RemotePane) IsPinned() bool    { return false }

func (rp *RemotePane) Activity() ActivityState {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	// Derive idle from output recency, same as local panes.
	if !rp.alive {
		return StateDead
	}
	if time.Since(rp.lastOut) >= ptyIdleTimeout {
		return StateIdle
	}
	return rp.activity
}

func (rp *RemotePane) LastOutputTime() time.Time {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.lastOut
}

func (rp *RemotePane) BeadID() string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.beadID
}

func (rp *RemotePane) SetBead(id, title string) {
	rp.mu.Lock()
	rp.beadID = id
	rp.mu.Unlock()
}

func (rp *RemotePane) SessionDesc() string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.sessDesc
}

func (rp *RemotePane) IdleWithBacklog() bool { return false }
func (rp *RemotePane) BacklogCount() int     { return 0 }

func (rp *RemotePane) Emulator() *vt.SafeEmulator { return rp.emu }

func (rp *RemotePane) GetRegion() Region { return rp.region }

// networkWriteTimeout is applied to all writes on yamux streams and control
// connections. Prevents the TUI from hanging when a remote daemon dies and
// the network write buffer fills up.
const networkWriteTimeout = 3 * time.Second

// SendKey encodes a tcell key event as raw ANSI bytes and writes them
// upstream to the daemon, which injects them into the remote PTY.
func (rp *RemotePane) SendKey(ev *tcell.EventKey) {
	var b []byte
	if ev.Key() == tcell.KeyRune {
		b = []byte(string(ev.Rune()))
	} else {
		b = tcellKeyToANSI(ev)
	}
	if len(b) > 0 {
		rp.stream.SetWriteDeadline(time.Now().Add(networkWriteTimeout))
		rp.stream.Write(b)
	}
}

// tcellKeyToANSI converts a non-rune tcell key event to its ANSI byte sequence.
func tcellKeyToANSI(ev *tcell.EventKey) []byte {
	switch ev.Key() {
	case tcell.KeyEnter:
		return []byte{'\r'}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return []byte{0x7f}
	case tcell.KeyTab:
		return []byte{'\t'}
	case tcell.KeyEscape:
		return []byte{0x1b}
	case tcell.KeyUp:
		return []byte("\x1b[A")
	case tcell.KeyDown:
		return []byte("\x1b[B")
	case tcell.KeyRight:
		return []byte("\x1b[C")
	case tcell.KeyLeft:
		return []byte("\x1b[D")
	case tcell.KeyHome:
		return []byte("\x1b[H")
	case tcell.KeyEnd:
		return []byte("\x1b[F")
	case tcell.KeyDelete:
		return []byte("\x1b[3~")
	case tcell.KeyPgUp:
		return []byte("\x1b[5~")
	case tcell.KeyPgDn:
		return []byte("\x1b[6~")
	case tcell.KeyCtrlC:
		return []byte{0x03}
	case tcell.KeyCtrlD:
		return []byte{0x04}
	case tcell.KeyCtrlZ:
		return []byte{0x1a}
	case tcell.KeyCtrlL:
		return []byte{0x0c}
	default:
		// Ctrl+letter: Ctrl+A=0x01, Ctrl+B=0x02, etc.
		if ev.Key() >= tcell.KeyCtrlA && ev.Key() <= tcell.KeyCtrlZ {
			return []byte{byte(ev.Key() - tcell.KeyCtrlA + 1)}
		}
		return nil
	}
}

// SendText sends text to the remote agent via the control channel. This uses
// the daemon's "send" command which injects text through the emulator path
// (same as initech send), handling Ctrl+S stash and paste detection.
func (rp *RemotePane) SendText(text string, enter bool) {
	// Fire-and-forget: network operations must never block the main loop.
	// The mux.Request has a 10s timeout; running it synchronously on the
	// main goroutine would freeze all rendering and input handling.
	go func() {
		_, err := rp.mux.Request(ControlCmd{
			Action: "send",
			Target: rp.name,
			Text:   text,
			Enter:  enter,
		})
		if err != nil {
			LogWarn("remote", "send failed", "agent", rp.name, "err", err)
		}
	}()
}

// Render draws the remote pane with [R] badge in the ribbon title.
func (rp *RemotePane) Render(screen tcell.Screen, focused bool, dimmed bool, index int, sel Selection) {
	r := rp.region
	if r.W < 1 || r.H < 2 {
		return
	}

	s := &clampedScreen{Screen: screen, r: r}

	trueBlack := tcell.NewRGBColor(0, 0, 0)
	ribbonY := r.Y + r.H - 1

	// Fill ribbon background.
	blackStyle := tcell.StyleDefault.Background(trueBlack)
	for x := r.X; x < r.X+r.W; x++ {
		s.SetContent(x, ribbonY, ' ', nil, blackStyle)
	}

	// Badge style: remote panes use magenta to distinguish from local.
	var titleStyle tcell.Style
	if focused {
		titleStyle = tcell.StyleDefault.Background(tcell.ColorDarkMagenta).Foreground(tcell.ColorWhite).Bold(true)
	} else {
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorDarkMagenta).Bold(true)
	}

	// Title: "N host:name [R]"
	displayName := rp.host + ":" + rp.name
	title := fmt.Sprintf(" %d %s [R] ", index, displayName)
	if !rp.IsAlive() {
		title = fmt.Sprintf(" %d %s [R][dead] ", index, displayName)
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorRed).Bold(true)
	}
	col := r.X + 1
	for _, ch := range title {
		if col < r.X+r.W {
			s.SetContent(col, ribbonY, ch, nil, titleStyle)
			col++
		}
	}

	// Bead ID in dark cyan.
	bead := rp.BeadID()
	if bead != "" {
		beadStr := "| " + bead + " "
		beadStyle := tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorDarkCyan)
		for _, ch := range beadStr {
			if col < r.X+r.W {
				s.SetContent(col, ribbonY, ch, nil, beadStyle)
				col++
			}
		}
	}

	// Drain pending byte chunks from readLoop and write to the emulator.
	// Both drain and cell reads happen on the main goroutine, so no mutex
	// is needed. This eliminates the deadlock/starvation that plagued every
	// mutex-based approach.
	//
	// Budget: limit bytes processed per frame to prevent stalls when the
	// ring buffer replays megabytes of historical data into a new pane.
	// Remaining data stays in dataCh for the next frame (33ms at 30fps).
	innerCols, innerRows := r.InnerSize()
	const drainBudget = 128 * 1024 // 128KB per pane per frame.
	drained := 0
	for drained < drainBudget {
		select {
		case chunk := <-rp.dataCh:
			rp.emu.Write(chunk)
			drained += len(chunk)
		default:
			goto drainDone
		}
	}
drainDone:
	emuRows := rp.emu.Height()
	for row := 0; row < innerRows; row++ {
		emuRow := emuRows - innerRows + row
		if emuRow < 0 || emuRow >= emuRows {
			continue
		}
		for c := 0; c < innerCols; c++ {
			cell := rp.emu.CellAt(c, emuRow)
			ch, style := uvCellToTcell(cell)
			if dimmed {
				style = dimStyle(style)
			}
			s.SetContent(r.X+c, r.Y+row, ch, nil, style)
		}
	}

	// Cursor.
	if focused && !sel.Active {
		pos := rp.emu.CursorPosition()
		visRow := pos.Y - (emuRows - innerRows)
		if pos.X >= 0 && pos.X < innerCols && visRow >= 0 && visRow < innerRows {
			cx := r.X + pos.X
			cy := r.Y + visRow
			cell := rp.emu.CellAt(pos.X, pos.Y)
			ch, _ := uvCellToTcell(cell)
			cursorStyle := tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack)
			s.SetContent(cx, cy, ch, nil, cursorStyle)
		}
	}
}

// Resize updates the local emulator immediately and debounces the control
// command to the remote daemon. Rapid resize events (SIGWINCH storms from
// dragging the terminal edge) are collapsed: only the final geometry is sent
// after a 50ms quiet period.
func (rp *RemotePane) Resize(rows, cols int) {
	// Emulator resize is synchronous: with the channel-based approach,
	// readLoop never touches the emulator, so no lock contention.
	// The emulator is owned exclusively by the main goroutine.
	rp.emu.Resize(cols, rows)

	rp.resizeMu.Lock()
	rp.pendingRows = rows
	rp.pendingCols = cols
	if rp.resizeTimer != nil {
		rp.resizeTimer.Stop()
	}
	rp.resizeTimer = time.AfterFunc(resizeDebounce, func() {
		rp.resizeMu.Lock()
		r, c := rp.pendingRows, rp.pendingCols
		rp.resizeMu.Unlock()
		rp.sendResize(r, c)
	})
	rp.resizeMu.Unlock()
}

// sendResize writes a resize control command to the daemon. Fire-and-forget:
// errors are logged but don't block. Called by the debounce timer goroutine.
func (rp *RemotePane) sendResize(rows, cols int) {
	_, err := rp.mux.Request(ControlCmd{
		Action: "resize",
		Target: rp.name,
		Rows:   rows,
		Cols:   cols,
	})
	if err != nil {
		LogDebug("remote", "resize failed (fire-and-forget)", "agent", rp.name, "err", err)
	}
}

// Close terminates the yamux stream and stops background goroutines.
func (rp *RemotePane) Close() {
	rp.resizeMu.Lock()
	if rp.resizeTimer != nil {
		rp.resizeTimer.Stop()
	}
	rp.resizeMu.Unlock()

	rp.mu.Lock()
	rp.alive = false
	rp.mu.Unlock()
	if rp.stream != nil {
		rp.stream.Close() // readLoop exits on stream read error.
	}
	// Close the emulator's input pipe so responseLoop's blocking Read exits.
	if pw, ok := rp.emu.InputPipe().(interface{ CloseWithError(error) error }); ok {
		pw.CloseWithError(io.EOF)
	}
	rp.goWg.Wait()
}

// ── Helpers ─────────────────────────────────────────────────────────

// extractSessionDesc reads the cursor row text as a session description,
// same logic as Pane but without the status bar filter (remote panes
// don't need the CUF bleed-through fix since the daemon's emulator
// already handles it).
func (rp *RemotePane) extractSessionDesc() {
	if rp.emu.IsAltScreen() {
		return
	}
	cols := rp.emu.Width()
	pos := rp.emu.CursorPosition()
	if pos.Y >= rp.emu.Height() {
		return
	}
	var desc strings.Builder
	for c := 0; c < cols; c++ {
		cell := rp.emu.CellAt(c, pos.Y)
		if cell != nil && cell.Content != "" {
			desc.WriteString(cell.Content)
		} else {
			desc.WriteByte(' ')
		}
	}
	trimmed := strings.TrimSpace(desc.String())
	if trimmed != "" && !strings.Contains(trimmed, "\u2502") {
		rp.mu.Lock()
		rp.sessDesc = trimmed
		rp.mu.Unlock()
	}
}

