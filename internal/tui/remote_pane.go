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
				// Safe: single writer goroutine guarantees no concurrent
				// push between drain and insert.
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
			LogDebug("remote-readloop", "stream ended",
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
func (rp *RemotePane) SubmitKey() string { return "" } // Remote panes use daemon-side config.

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
	} else if ev.Key() == tcell.KeyEnter && ev.Modifiers()&tcell.ModShift != 0 {
		// Shift+Enter: CSI-u encoded (ESC[13;2u). See Pane.SendKey for rationale.
		b = []byte("\x1b[13;2u")
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
	case tcell.KeyBacktab:
		// Shift+Tab: ESC [ Z is the standard reverse-tab ANSI sequence.
		return []byte("\x1b[Z")
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

// DrainData moves pending byte chunks from dataCh into the emulator. Called
// by the TUI main loop for ALL remote panes (visible or hidden) so hidden
// panes don't accumulate stale data. Budget limits bytes per call to prevent
// stalls when the ring buffer replays megabytes into a new pane.
func (rp *RemotePane) DrainData() {
	const drainBudget = 128 * 1024 // 128KB per pane per tick.
	drained := 0
	for drained < drainBudget {
		select {
		case chunk := <-rp.dataCh:
			rp.emu.Write(chunk)
			drained += len(chunk)
		default:
			return
		}
	}
}

// Render draws the remote pane with [R] badge in the ribbon title.
func (rp *RemotePane) Render(screen tcell.Screen, focused bool, dimmed bool, index int, sel Selection) {
	r := rp.region
	if r.W < 1 || r.H < 2 {
		return
	}

	s := &clampedScreen{Screen: screen, r: r}

	// Badge style: remote panes use teal to distinguish from local.
	var titleStyle tcell.Style
	if focused {
		titleStyle = tcell.StyleDefault.Background(tcell.ColorTeal).Foreground(tcell.ColorBlack).Bold(true)
	} else {
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorTeal).Bold(true)
	}

	displayName := rp.host + ":" + rp.name
	title := fmt.Sprintf(" %d %s [R] ", index, displayName)
	if !rp.IsAlive() {
		title = fmt.Sprintf(" %d %s [R][dead] ", index, displayName)
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorRed).Bold(true)
	}

	renderRibbon(s, r, title, titleStyle, rp.BeadID())

	_, innerRows := r.InnerSize()
	emuStartRow := rp.emu.Height() - innerRows
	renderCells(s, r, rp.emu, dimmed, emuStartRow)
	renderSelection(s, r, rp.emu, sel, dimmed, emuStartRow)
	renderCursor(s, r, rp.emu, focused, sel, emuStartRow)
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
// Uses a timeout to prevent hanging on dead yamux streams during shutdown.
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
	// Wait for goroutines with a timeout. Dead yamux streams can cause
	// stream.Read and stream.Close to block indefinitely after a remote
	// server restart (half-open TCP connection).
	done := make(chan struct{})
	go func() {
		rp.goWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		LogWarn("remote", "Close timed out waiting for goroutines", "agent", rp.name, "host", rp.host)
	}
}


