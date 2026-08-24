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
	name     string           // Agent name (e.g. "eng1").
	host     string           // Peer name of the remote daemon (e.g. "workbench").
	stream   net.Conn         // Yamux stream: downstream PTY bytes + upstream keystrokes.
	mux      *ControlMux      // Shared multiplexed control channel (thread-safe).
	emu      *vt.SafeEmulator // Local VT emulator owned exclusively by the main goroutine.
	dataCh   chan []byte      // readLoop sends byte chunks here; Render drains and writes to emu.
	mu       sync.Mutex
	alive    bool
	fleetNum int // Fleet-canonical number PLUS ONE (ini-6m4); zero value = unknown. See fleetNumbered in pane.go.
	// emuPanicked rate-limits the emulator-panic log to once per pane; the
	// panic barrier in writeEmu keeps draining after a parser failure.
	emuPanicked bool

	// Desync/resync state (ini-dr03). readLoop sets these; DrainData, which
	// runs on the main goroutine that OWNS emu, acts on them. readLoop must
	// never touch emu itself.
	resyncPending bool      // a request is in flight; coalesces a burst into one
	resetPending  bool      // main goroutine must clear emu before applying more bytes
	lastResync    time.Time // rate-limit floor, so sustained load cannot storm
	visible       bool
	activity      ActivityState
	lastOut       time.Time
	beadIDs       []string
	sessDesc      string

	// waiting is window 1's needs-input state for this agent, pushed over the
	// wire (ini-35ak). A viewer never derives it: it cannot see the agent's
	// PTY, so the authority that watches the dialog is the only process that
	// can know.
	waiting WaitingState
	// suspended is the authority's word that this agent is parked
	// (agent_status broadcast) — without it a parked agent's silent stream
	// reads as idle in every window but the one that parked it.
	suspended bool
	region    Region

	goWg sync.WaitGroup // Tracks readLoop goroutine. Close waits on this.

	// Resize debounce: pendingResize holds the latest requested dimensions.
	// The timer fires after resizeDebounce and sends the final geometry.
	resizeMu    sync.Mutex
	resizeTimer *time.Timer
	pendingRows int
	pendingCols int

	// lastLoggedH/W gate the render-region trace to changes only.
	lastLoggedH int
	lastLoggedW int
}

// Mux exposes the shared control multiplexer for this RemotePane's peer
// connection. Callers (e.g. :remote-stop) use it to send control commands
// such as stop_agent that target the remote daemon directly.
func (rp *RemotePane) Mux() *ControlMux { return rp.mux }

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
		visible:  true,
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
				// CHANNEL FULL. Dropping the oldest chunk silently is the
				// defect ini-dr03 removes: a chunk boundary is a NETWORK READ
				// boundary, not a terminal-grammar one, so an evicted chunk
				// can carry away the first half of a CSI sequence and leave
				// the second half to render as literal text. Measured
				// reachable under ordinary chatty-agent output (qa1).
				//
				// We still evict rather than block. REFUSING TO READ IS
				// BACKPRESSURE, which propagates through yamux to the
				// daemon's MultiSink writer; post-v2.11.3 a persistently
				// timing-out writer is dropped after ~21s and nothing re-adds
				// a dropped stream. That is the v2.11.2 FREEZE, reintroduced
				// from the receive side -- the reason backpressure was
				// rejected for this bead. Liveness is preserved here and
				// correctness is restored by the resync below.
				rp.noteDesync()
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

// Visible returns whether the remote pane should appear in the layout.
func (rp *RemotePane) Visible() bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.visible
}

// SetVisible controls whether the remote pane appears in the layout.
func (rp *RemotePane) SetVisible(v bool) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.visible = v
}

// FleetIdx implements fleetNumbered: the position window 1 listed this agent
// at in hello_ok, which is window 1's pane creation order -- the same sequence
// window 1's own modal numbers by. -1 when the server predates the ordering.
func (rp *RemotePane) FleetIdx() int { return rp.fleetNum - 1 }

func (rp *RemotePane) IsAlive() bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.alive
}

// IsSuspended reports the authority's broadcast state (ApplySuspended) — it
// was a hardcoded false before suspension crossed the window boundary.
func (rp *RemotePane) IsSuspended() bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.suspended
}
func (rp *RemotePane) IsProtected() bool              { return false }
func (rp *RemotePane) AgentType() string              { return "" } // Remote panes do not currently expose daemon-side agent type.
func (rp *RemotePane) SubmitKey() string              { return "" } // Remote panes use daemon-side config.
func (rp *RemotePane) ActiveRunStart() time.Time      { return time.Time{} }
func (rp *RemotePane) ActiveRunBytes() int64          { return 0 }
func (rp *RemotePane) LastMessageReceived() time.Time { return time.Time{} }
func (rp *RemotePane) LastEventTime() time.Time       { return time.Time{} }

func (rp *RemotePane) Activity() ActivityState {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	// Suspension is the authority's word (agent_status), checked before the
	// recency heuristics: a parked agent produces no output, which reads as
	// idle — precisely the misread that hid eng2's state from window 2.
	if rp.suspended {
		return StateSuspended
	}
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
	if len(rp.beadIDs) == 0 {
		return ""
	}
	return rp.beadIDs[0]
}

func (rp *RemotePane) BeadIDs() []string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.beadIDs) == 0 {
		return nil
	}
	out := make([]string, len(rp.beadIDs))
	copy(out, rp.beadIDs)
	return out
}

func (rp *RemotePane) SetBead(id, title string) {
	rp.mu.Lock()
	if id == "" {
		rp.beadIDs = nil
	} else {
		rp.beadIDs = []string{id}
	}
	rp.mu.Unlock()
}

func (rp *RemotePane) SetBeads(ids []string) {
	rp.mu.Lock()
	if len(ids) == 0 {
		rp.beadIDs = nil
	} else {
		rp.beadIDs = make([]string, len(ids))
		copy(rp.beadIDs, ids)
	}
	rp.mu.Unlock()
}

func (rp *RemotePane) SessionDesc() string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.sessDesc
}

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
		if ev.Modifiers()&tcell.ModAlt != 0 {
			// Meta-encode Alt+rune (ESC prefix), matching what the local
			// pane's tcellKeyToUV path puts on the child's PTY. Writing the
			// bare rune here dropped the modifier: Claude Code's Option+P
			// model selector worked on window 1 and was dead on every viewer
			// (operator, 2026-08-16).
			b = []byte("\x1b" + string(ev.Rune()))
		} else {
			b = []byte(string(ev.Rune()))
		}
	} else if ev.Key() == tcell.KeyEnter && ev.Modifiers()&tcell.ModShift != 0 {
		// Shift+Enter: CSI-u encoded. See Pane.SendKey for rationale.
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

// minResyncInterval is the floor between two resync requests for one pane.
//
// COALESCING IS REQUIRED, not an optimisation (ini-dr03 edge case). The
// condition that evicts a chunk is a burst, and a burst evicts many chunks --
// one request per eviction would send hundreds, and every replay is a full
// ring-buffer snapshot written back down the same stream, which is itself a
// large write that helps fill the channel again. Requesting per eviction turns
// a corrupted pane into a resync storm that sustains its own cause.
const minResyncInterval = 2 * time.Second

// noteDesync records that a chunk was evicted and asks the daemon to replay.
//
// Called from readLoop, so it must not block and must not touch emu: it sets
// flags and hands the request to its own goroutine. A blocking request here
// would stall stream consumption, which is backpressure by another name.
func (rp *RemotePane) noteDesync() {
	rp.mu.Lock()
	if rp.resyncPending || time.Since(rp.lastResync) < minResyncInterval {
		rp.mu.Unlock()
		return
	}
	rp.resyncPending = true
	rp.resetPending = true
	rp.lastResync = time.Now()
	rp.mu.Unlock()

	// INFO, not Debug (ini-dr03 edge case, and the ini-9gvn rule it inherits):
	// a silent resync loop is the same invisibility class as a withheld
	// submit -- the pane looks fine, the operator sees a flicker, and nothing
	// in the log says the viewer lost bytes.
	LogInfo("remote", "pane DESYNCED: buffered chunk evicted, requesting replay",
		"agent", rp.name, "host", rp.host)
	go rp.requestResync()
}

// requestResync asks the daemon to replay this agent's ring buffer to us.
func (rp *RemotePane) requestResync() {
	defer func() {
		rp.mu.Lock()
		rp.resyncPending = false
		rp.mu.Unlock()
	}()
	if rp.mux == nil {
		return
	}
	// The CONTROL channel, never the data stream: the data stream's upstream
	// direction is raw keystrokes into the agent's PTY, so a request sent
	// there would be TYPED BY THE AGENT.
	if _, err := rp.mux.Request(ControlCmd{Action: "resync", Target: rp.name}); err != nil {
		LogWarn("remote", "resync request failed; pane stays desynced until the next eviction",
			"agent", rp.name, "err", err)
		return
	}
	LogInfo("remote", "resync requested", "agent", rp.name)
}

// discardBuffered empties dataCh without applying it.
func (rp *RemotePane) discardBuffered() int {
	n := 0
	for {
		select {
		case <-rp.dataCh:
			n++
		default:
			return n
		}
	}
}

// DrainData moves pending byte chunks from dataCh into the emulator. Called
// by the TUI main loop for ALL remote panes (visible or hidden) so hidden
// panes don't accumulate stale data. Budget limits bytes per call to prevent
// stalls when the ring buffer replays megabytes into a new pane.
func (rp *RemotePane) DrainData() {
	// A resync was requested: the replay coming down the stream is HISTORY,
	// and applying history on top of newer bytes is the stale-replay case
	// (ini-dr03 edge case). Rather than order the two, hold NOTHING -- drop
	// what is buffered and clear the screen, so whatever arrives next is the
	// whole truth. The daemon snapshots after our request reaches it, so the
	// snapshot contains everything written before that moment and anything
	// later arrives later on the same FIFO stream.
	rp.mu.Lock()
	reset := rp.resetPending
	rp.resetPending = false
	rp.mu.Unlock()
	if reset {
		dropped := rp.discardBuffered()
		// RIS. The emulator has no Reset method, so the reset goes through
		// the parser the same way a real terminal would receive it.
		rp.writeEmu([]byte("\x1bc"))
		LogInfo("remote", "pane RESET for replay", "agent", rp.name, "chunks_discarded", dropped)
	}

	const drainBudget = 128 * 1024 // 128KB per pane per tick.
	drained := 0
	for drained < drainBudget {
		select {
		case chunk := <-rp.dataCh:
			rp.writeEmu(chunk)
			drained += len(chunk)
		default:
			return
		}
	}
}

// writeEmu feeds one chunk to the emulator with a panic barrier (ini-6m4).
//
// The emulator is a parser of bytes another process produced; a parser bug
// must cost AT MOST one pane's rendering, never the window. Without this,
// an out-of-range scroll operation in the vt fork (ini-w6z: 53-row Claude
// replay into a 24-row buffer) panicked the whole viewer process, which then
// relaunched and crashed again -- the operator's 'window 2 disconnects every
// few seconds' was this loop.
//
// Deliberately does NOT mark the pane dead: waitForDisconnect treats an
// all-dead pane set as a lost peer, so converting every panicking pane to
// dead would tear down a healthy session and recreate the reconnect loop by
// a different road (the ini-1ch shape). The pane stays alive with possibly
// garbled content, the defect is logged once per pane, and later chunks keep
// flowing -- rendering may recover, and the real fix is the fork's clamp.
func (rp *RemotePane) writeEmu(chunk []byte) {
	defer func() {
		if r := recover(); r != nil {
			rp.mu.Lock()
			logIt := !rp.emuPanicked
			rp.emuPanicked = true
			rp.mu.Unlock()
			if logIt {
				LogError("remote-pane", "emulator panicked on remote bytes; pane rendering degraded, window unaffected",
					"agent", rp.name, "host", rp.host, "panic", fmt.Sprint(r))
			}
		}
	}()
	rp.emu.Write(chunk)
}

// Render draws the remote pane with [R] badge in the ribbon title.
func (rp *RemotePane) Render(screen tcell.Screen, focused bool, dimmed bool, index int, sel Selection) {
	r := rp.region
	if r.W < 1 || r.H < 2 {
		return
	}

	s := &clampedScreen{Screen: screen, r: r}

	// Change-gated geometry trace (post-wake garble instrumentation): the
	// region this pane is DRAWN at vs the emulator it draws FROM. A split
	// between this and the last "viewer resize" line is the defect.
	if r.H != rp.lastLoggedH || r.W != rp.lastLoggedW {
		rp.lastLoggedH, rp.lastLoggedW = r.H, r.W
		LogInfo("remote", "render region", "agent", rp.name, "host", rp.host,
			"region_h", r.H, "region_w", r.W,
			"emu_h", rp.emu.Height(), "emu_w", rp.emu.Width())
	}

	// Badge style: remote panes use teal to distinguish from local.
	var titleStyle tcell.Style
	if focused {
		titleStyle = tcell.StyleDefault.Background(tcell.ColorTeal).Foreground(tcell.ColorBlack).Bold(true)
	} else {
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorTeal).Bold(true)
	}

	// THE RIBBON IS THE THIRD DISPLAY SITE of the window-alias prefix, and the
	// one a text search for the composition pattern does not find, because it
	// builds the name here rather than at the surface that draws it (ini-9isx
	// AC1 names overlay, modal AND ribbon). The composed two-window rig is what
	// found it: window 2's ribbon read "1 window1:eng1 [R]" while its overlay
	// was already clean.
	displayName := paneDisplayName(rp)
	badge := " [R] "
	if !paneIsRemoteMachine(rp) {
		// Same correction as the overlay's badge: an agent reached through
		// window 1 is not on another machine, and saying so twice over (prefix
		// and badge) was the clutter compounded.
		badge = " "
	}
	title := fmt.Sprintf(" %d %s%s", index, displayName, badge)
	if rp.Activity() == StateSuspended {
		// Same badge, same taught gesture as the local ribbon: the stream
		// pump swallows input at a suspended pane as a wake (ini-zffi's
		// discoverability AC applies wherever the pane is rendered).
		title = fmt.Sprintf(" %d %s%s[susp: any key] ", index, displayName, badge)
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorDodgerBlue).Bold(true)
	} else if !rp.IsAlive() {
		title = fmt.Sprintf(" %d %s%s[dead] ", index, displayName, badge)
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorRed).Bold(true)
	}

	// Remote panes don't carry the local lastOutputTime/tint-hold state, so
	// the running-pane tint is local-only for v1 (ini-zmzg). The badge's
	// running square (ini-z9a3) reuses that exact tint signal by design, so
	// it's local-only for the same reason here — never a fabricated parallel
	// "running" notion for remote panes.
	renderRibbon(s, r, title, titleStyle, rp.BeadID(), "", false)

	_, innerRows := r.InnerSize()
	emuStartRow := rp.emu.Height() - innerRows
	renderCells(s, r, rp.emu, dimmed, emuStartRow, tcell.ColorDefault)
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
	if rp.mux == nil {
		return
	}
	LogInfo("remote", "viewer resize", "agent", rp.name, "host", rp.host,
		"rows", rows, "cols", cols, "viewer_emu_h", rp.emu.Height(), "viewer_emu_w", rp.emu.Width())
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

// ApplyStatus updates this remote pane's observed agent state -- the bead it
// holds and its session description -- from window 1 (ini-9ka.11).
//
// Before this existed, beadIDs was written ONLY by the local process's own IPC
// and sessDesc had no writer at all, so a secondary window showed no bead and
// no description for any agent: absent values, not stale ones. The data was
// already arriving in the hello_ok handshake (AgentStatus.Bead) and being
// discarded; this is the missing write.
//
// PLURAL, deliberately: agents routinely hold several beads and the ribbon
// renders all of them. A singular signature here would truncate to one and
// display a WRONG-but-populated value, which is harder to notice than the
// empty field this bead exists to fix.
//
// Empty is meaningful and applied as-is: clearing beads is a real state change
// an operator must see, so this must not treat empty as "no update". Callers
// that mean "no update" simply do not call. The slice is copied so a caller
// reusing its buffer cannot mutate this pane's state afterwards.
// WaitingInput reports the needs-input state window 1 pushed for this agent,
// satisfying waitingPane (ini-35ak).
//
// THIS METHOD IS THE BEAD. waitingRows and shouldChime both do p.(waitingPane)
// and skip anything that does not implement it, so before this existed every
// remote pane was skipped BEFORE scoping was ever consulted -- which is why a
// viewer had never chimed or listed a window-1 agent in any release. The walk
// itself needed no change; it only needed remote panes to be able to answer.
//
// Returns the same shape as *Pane's: a zero time when not waiting.
func (rp *RemotePane) WaitingInput() (bool, time.Time, string) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.waiting.Waiting, rp.waiting.Since(), rp.waiting.Preview
}

// ApplyWaiting records what window 1 last said about this agent's wait.
//
// Assignment, not merge: the authority's latest word is the whole truth, and
// the CLEAR edge is just a WaitingState whose Waiting is false. Treating the
// clear as a special case is how a stale row survives an answered dialog.
func (rp *RemotePane) ApplyWaiting(ws WaitingState) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.waiting = ws
}

// ApplySuspended records the authority's word on whether this agent is
// parked. Assignment both edges, same doctrine as ApplyWaiting: a wake is
// the same detector seeing false, not a clear path beside a park path.
func (rp *RemotePane) ApplySuspended(suspended bool) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.suspended = suspended
}

func (rp *RemotePane) ApplyStatus(beads []string, desc string) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(beads) == 0 {
		rp.beadIDs = nil
	} else {
		rp.beadIDs = append([]string(nil), beads...)
	}
	rp.sessDesc = desc
}

// FlushPaste delivers a buffered paste to the agent on the far side,
// UNSUBMITTED (ini-a9d8).
//
// It goes through the daemon's send handler rather than writing bracketed
// paste bytes straight to rp.stream the way SendKey does, and that choice is
// the load-bearing one. Writing the stream directly would look more like
// local paste and would BYPASS the suspension guard that ini-g7fl moved down
// into SendText precisely so no entry point has to remember it -- which is the
// ini-9imx defect exactly: a caller that skips the primitive loses the guard
// and reports success while the text vanishes. Routing through "send"
// inherits the suspension queue and the modal deferral for free.
//
// enter is false and must stay false: a paste lands in the composer and the
// operator presses enter themselves. That also keeps this clear of ini-vpwg's
// never-submit belt, which lives below "if !enter { return }" in
// sendPaneTextLocked and is unreachable by construction from here.
func (rp *RemotePane) FlushPaste(content []byte) {
	if len(content) == 0 {
		return
	}
	text := string(content)
	// Fire-and-forget for the same reason SendText is: a network round trip on
	// the main goroutine would freeze rendering and input.
	go func() {
		resp, err := rp.mux.Request(ControlCmd{
			Action: "send",
			Target: rp.name,
			Text:   text,
			Enter:  false,
		})
		if err != nil {
			LogWarn("remote", "paste failed in transit", "agent", rp.name,
				"bytes", len(text), "err", err)
			return
		}
		// THE RESPONSE IS READ, not discarded. The daemon rejects a send whose
		// target is unknown or whose body exceeds maxSendTextLen (64 KB) by
		// returning ControlResp{Error} with a nil transport error -- so
		// throwing the response away, as SendText does, discards a >64KB paste
		// exactly as silently as the bug this fixes. A swallowed paste is the
		// same invisibility class as a withheld submit (ini-9gvn), so it is
		// loud or it is a repeat of the same defect.
		if resp.Error != "" {
			LogWarn("remote", "PASTE NOT DELIVERED", "agent", rp.name,
				"bytes", len(text), "reason", resp.Error)
			return
		}
		LogInfo("remote", "paste delivered to viewer pane", "agent", rp.name, "bytes", len(text))
	}()
}
