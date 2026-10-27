package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/x/vt"
	"io"
	"net"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/nmelo/initech/internal/config"
)

const (
	rawSubmitDelay            = 200 * time.Millisecond
	bracketedPasteSubmitDelay = 500 * time.Millisecond
	codexBracketedSubmitDelay = 200 * time.Millisecond
)

// IPCRequest is the JSON structure sent by CLI commands to the TUI socket.
type IPCRequest struct {
	Action      string `json:"action"`                 // "send", "peek", "list", "peers_query"
	Target      string `json:"target"`                 // Role name (for send/peek).
	Host        string `json:"host"`                   // Remote peer name (for cross-machine send). Empty = local.
	Text        string `json:"text"`                   // Text to inject (for send).
	Lines       int    `json:"lines"`                  // Number of lines to return (for peek, 0 = all).
	Enter       bool   `json:"enter"`                  // Append Enter after text (for send).
	Prune       bool   `json:"prune,omitempty"`        // reload: also remove deconfigured self-started agents.
	ItemID      string `json:"item_id,omitempty"`      // Inbox item, distinct from control request correlation.
	DefaultText string `json:"default_text,omitempty"` // Proposed action absent an operator reply.
	Chime       bool   `json:"chime,omitempty"`        // Request one inbox arrival chime.
	Detail      bool   `json:"detail,omitempty"`       // list: also read each pane's footer and dialog (status --json).
}

// IPCResponse is the JSON structure returned by the TUI socket.
type IPCResponse struct {
	Notices    []string          `json:"notices,omitempty"` // Teaching lines, printed after the normal result; never overwrite another detector.
	WindowPort *WindowPortStatus `json:"window_port,omitempty"`
	Project    *IPCProject       `json:"project,omitempty"` // The serving process's project, on list (ini-l8s5).
	OK         bool              `json:"ok"`
	Error      string            `json:"error,omitempty"`
	Data       string            `json:"data,omitempty"`     // Pane content for peek, pane list for list.
	Delivery   string            `json:"delivery,omitempty"` // A send's verdict (ini-69fy): a Delivery* outcome, its reason in Data.
}

// IPCProject names the project behind a socket, so a caller that reached it
// through INITECH_SOCKET labels what it reads with that project rather than
// its own folder's (ini-l8s5). An older server sends none.
type IPCProject struct {
	Name         string `json:"name"`
	BeadsEnabled bool   `json:"beads_enabled"`
	// Root is the project's folder, where its bd database is read from
	// (ini-lg62). Omitted when unknown.
	Root string `json:"root,omitempty"`
}

// projectSnapshot is the IPCProject for a loaded project, nil without one.
func projectSnapshot(p *config.Project) *IPCProject {
	if p == nil {
		return nil
	}
	return &IPCProject{Name: p.Name, BeadsEnabled: p.Beads.IsEnabled(), Root: p.Root}
}

// ProjectSnapshot is the project this TUI serves, for the list reply.
func (t *TUI) ProjectSnapshot() *IPCProject { return projectSnapshot(t.project) }

// SocketPath returns the IPC endpoint path for a project. On Unix this is a
// domain socket inside .initech/; on Windows a port file names a loopback TCP endpoint.
func SocketPath(projectRoot, projectName string) string {
	return socketPath(projectRoot, projectName)
}

// startIPC launches the IPC server in a goroutine.
// Returns a cleanup function that closes the listener and removes the endpoint.
func (t *TUI) startIPC(sp string) (cleanup func(), err error) {
	ln, err := listenIPC(sp)
	if err != nil {
		return nil, err
	}

	t.safeGo(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.safeGo(func() { t.handleIPCConn(conn) })
		}
	})

	cleanup = func() {
		ln.Close()
		os.Remove(sp)
	}
	return cleanup, nil
}

func (t *TUI) handleIPCConn(conn net.Conn) {
	defer conn.Close()

	// Prevent goroutine leak from clients that connect but never send data.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := NewIPCScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		LogWarn("ipc", "invalid JSON from client", "err", err)
		writeIPCResponse(conn, IPCResponse{Error: "invalid JSON"})
		return
	}
	LogDebug("ipc", "request", "action", req.Action, "target", req.Target)

	dispatchIPC(t, conn, req, scanner.Bytes())
}

// ── IPCHost implementation ─────────────────────────────────────────

func (t *TUI) FindPaneView(name string) (PaneView, bool) {
	var pv PaneView
	ok := t.runOnMain(func() { pv = t.findPaneByName(name) })
	return pv, ok
}

func (t *TUI) AllPanes() ([]PaneInfo, bool) {
	panes, _, ok := t.allPanesWithViews()
	return panes, ok
}

// allPanesWithViews is AllPanes plus the pane behind each entry, taken in the
// same main-loop turn so the two cannot disagree.
func (t *TUI) allPanesWithViews() ([]PaneInfo, []PaneView, bool) {
	var panes []PaneInfo
	var views []PaneView
	ok := t.runOnMain(func() {
		panes = make([]PaneInfo, len(t.panes))
		views = make([]PaneView, len(t.panes))
		for i, p := range t.panes {
			views[i] = p
			panes[i] = PaneInfo{
				Name:     p.Name(),
				Host:     p.Host(),
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[agentKey(p)],
				Beads:    ribbonBeads(p),
				NoBead:   noBeadOf(p),
				Busy:     paneBusy(p, time.Now()),
			}
			// The latch is why mail stops moving, so it belongs where someone
			// looks when mail stops moving (ini-gbqc). Age and depth, not a
			// bare flag: the live incident was diagnosed by a human peeking at
			// a pane because status could not say a dialog had been up for
			// hours with four messages behind it.
			if lp, ok := p.(*Pane); ok {
				if age, latched := lp.latchAge(time.Now()); latched {
					panes[i].ModalLatched = true
					panes[i].ModalAgeSec = int(age.Seconds())
					panes[i].QueuedCount = lp.QueuedMessageCount()
				}
			}
		}
	})
	return panes, views, ok
}

func (t *TUI) HandleSend(conn net.Conn, req IPCRequest) {
	t.handleIPCSend(conn, req)
}

func (t *TUI) HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool {
	switch req.Action {
	case "post", "post_check", "post_mine", "post_withdraw":
		t.handleIPCPost(conn, req, rawJSON)
	case "stop":
		t.handleIPCStop(conn, req)
	case "start":
		t.handleIPCStart(conn, req)
	case "send_ready":
		t.handleIPCSendReady(conn, req)
	case "restart":
		t.handleIPCRestart(conn, req)
	case "reload":
		t.handleIPCReload(conn, req)
	case "bead":
		t.handleIPCBead(conn, req)
	case "no_bead":
		t.handleIPCNoBead(conn, req)
	case "patrol":
		t.handleIPCPatrol(conn, req)
	case "add":
		t.handleIPCAdd(conn, req)
	case "remove":
		t.handleIPCRemove(conn, req)
	case "interrupt":
		t.handleIPCInterrupt(conn, req)
	case "resume":
		t.handleIPCResume(conn, req)
	case "suspend":
		t.handleIPCSuspend(conn, req)
	case "attention":
		t.handleIPCAttention(conn, req)
	case "emit_event":
		t.handleIPCEmitEvent(conn, req)
	case "peers_query":
		t.handleIPCPeers(conn)
	case "quit":
		t.handleIPCQuit(conn)
	default:
		return false
	}
	return true
}

// maxSendTextLen is the maximum size of text accepted by the send IPC action.
// Prevents a misbehaving client from injecting megabytes of keystroke data
// that would freeze the TUI while processing rune-by-rune under sendMu.
const maxSendTextLen = 64 * 1024 // 64 KB

// IPCScanBufSize is the buffer limit for all IPC and control-stream scanners.
// Must exceed maxSendTextLen plus JSON framing overhead so that a legal
// near-limit send is tokenized successfully before the explicit size check
// runs. 256 KB gives ~4x headroom over the 64 KB text limit (ini-piyb.2).
const IPCScanBufSize = 256 * 1024

// NewIPCScanner creates a bufio.Scanner with a buffer large enough to handle
// the largest supported IPC/control message (maxSendTextLen + JSON framing).
func NewIPCScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, IPCScanBufSize), IPCScanBufSize)
	return s
}

// handleIPCSendReady answers whether a send to the target would be delivered
// now, waiting on the same startup gate a send waits on (ini-i35w). assign
// asks this BEFORE it claims anything, so a booting agent never ends up with
// a claimed bead and no message. Only a local pane can be judged here: a
// suspended one is fine (the send queues and wakes it), and a remote target
// is applied by the window that owns it.
func (t *TUI) handleIPCSendReady(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	var pv PaneView
	var notRunning error
	if !t.runOnMain(func() {
		pv = t.findPaneByName(req.Target)
		if lp, ok := pv.(*Pane); ok {
			notRunning = lp.notRunningReason()
		}
	}) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	// A stopped or exited agent is not ready, so assign claims nothing
	// (ini-2s7q).
	if notRunning != nil {
		writeIPCResponse(conn, IPCResponse{Error: notRunning.Error()})
		return
	}
	if lp, ok := pv.(*Pane); ok && !lp.IsSuspended() {
		if err := lp.awaitStartupSettled(); err != nil {
			writeIPCResponse(conn, IPCResponse{Error: err.Error()})
			return
		}
	}
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) handleIPCSend(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	if len(req.Text) > maxSendTextLen {
		writeIPCResponse(conn, IPCResponse{Error: "text too large (max 64KB)"})
		return
	}

	// Cross-machine routing: if Host is set, look up by host:agent.
	// Empty host or host matching our own peer name resolves locally.
	if req.Host != "" {
		localPeerName := ""
		if t.project != nil {
			localPeerName = t.project.PeerName
		}
		if req.Host != localPeerName {
			t.forwardSendToRemote(conn, req)
			return
		}
	}

	// Look up the pane. The suspended-pane path needs *Pane for EnqueueMessage
	// and resumePane; the normal path uses PaneView.SendText.
	var pv PaneView
	var concretePane *Pane
	var queued bool
	var notRunning error
	if !t.runOnMain(func() {
		pv = t.findPaneByName(req.Target)
		if pv == nil {
			return
		}
		// A process that is gone gets nothing typed and the sender is told
		// (ini-2s7q). Read here, with the suspended check, on main.
		if lp, ok := pv.(*Pane); ok {
			if notRunning = lp.notRunningReason(); notRunning != nil {
				return
			}
		}
		if lp, ok := pv.(*Pane); ok && lp.suspended {
			concretePane = lp
			dropped := lp.EnqueueMessage(req.Text, req.Enter)
			if dropped {
				t.notifications = append(t.notifications, notification{
					event: AgentEvent{
						Type:   EventAgentStalled,
						Pane:   lp.name,
						Detail: "Message queue full, oldest message dropped.",
						Time:   time.Now(),
					},
					expires: time.Now().Add(notificationTTL),
				})
			}
			queued = true
		}
	}) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	// A bare name on another machine's or window's agent takes the host:agent
	// route (ini-69fy G7): the owner answers with its own verdict. Its
	// SendText answers nothing.
	if rp, ok := pv.(*RemotePane); ok {
		t.sendViaRemote(conn, rp, req)
		return
	}
	if notRunning != nil {
		LogInfo("ipc", "SEND NOT DELIVERED", "target", req.Target, "reason", notRunning)
		writeIPCResponse(conn, IPCResponse{Error: notRunning.Error()})
		return
	}
	if queued {
		// Trigger resume-on-message: respawn the agent and drain the queue.
		// This blocks until the agent initializes and messages are delivered.
		if err := t.resumePane(concretePane, "incoming message"); err != nil {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("resume failed: %v", err)})
			return
		}
		writeIPCResponse(conn, resumedSendReply())
		return
	}

	// A sender is waiting on this answer, so a pane still booting REFUSES here,
	// before anything is written (ini-i35w): text sent into a booting Claude
	// is eaten while the sender is told it was delivered. initech send and
	// assign then exit non-zero, and assign rolls its claim back.
	if lp, ok := pv.(*Pane); ok {
		if err := lp.awaitStartupSettled(); err != nil {
			LogInfo("ipc", "SEND NOT DELIVERED", "target", req.Target, "reason", err)
			writeIPCResponse(conn, IPCResponse{Error: err.Error()})
			return
		}
	}

	// Age and depth travel with the deferral (ini-gbqc): "deferred" alone
	// cannot tell a sender a dialog opened a second ago from one that has held
	// their mail for hours, and the second is the case worth acting on. The
	// live report was hours of silent deferral, diagnosed only by a human
	// peeking at the pane.
	var latchAge time.Duration
	var queuedDepth int
	if lp, ok := pv.(*Pane); ok {
		t.runOnMain(func() {
			latchAge, _ = lp.latchAge(time.Now())
			queuedDepth = lp.QueuedMessageCount()
		})
	}

	// Normal path: deliver via PaneView.SendText (works for local and remote).
	//
	// The efficiency rig's delivery probe (ini-pqdy.4): a send carrying an
	// IQPERF token is stamped as accepted, armed BEFORE the write so its echo
	// cannot race ahead, and stamped again once written. Local panes only.
	marker := perfMarkerToken(req.Text)
	if lp, ok := pv.(*Pane); ok && marker != "" {
		LogInfo("perf", "marker accepted", "pane", req.Target, "marker", marker)
		lp.armPerfMarker(marker)
	}
	// The reply waits for the verdict and carries it (ini-69fy): the send
	// path decides it as it delivers, so a deferral is the send path's own
	// (ini-7txh), not a guess made before it ran. A PaneView that is neither
	// kind answers what it can: nothing checked.
	verdict := SendVerdict{DeliveryUnchecked, uncheckedVerdictReason}
	if lp, ok := pv.(*Pane); ok {
		verdict = lp.SendTextVerdict(req.Text, req.Enter)
	} else {
		pv.SendText(req.Text, req.Enter)
	}
	resp := verdictIPCResponse(req.Target, verdict, deferredMsg(req.Target, latchAge, queuedDepth))
	if verdict.Outcome == DeliveryNotDelivered {
		LogInfo("ipc", "SEND NOT DELIVERED", "target", req.Target, "reason", verdict.Reason)
		writeIPCResponse(conn, resp)
		return
	}
	if verdict.Outcome == DeliveryDeferred {
		writeIPCResponse(conn, resp)
		return
	}

	// Log the send event (no toast, too frequent).
	preview := req.Text
	if len(preview) > 60 {
		preview = preview[:57] + "..."
	}
	t.runOnMain(func() {
		t.logEvent(AgentEvent{
			Type:   EventMessageSent,
			Pane:   req.Target,
			Detail: "Message sent to " + req.Target + ": " + preview,
		})
	})
	writeIPCResponse(conn, resp)
}

// resumedSendReply answers a send that woke a suspended agent and delivered
// its queue.
func resumedSendReply() IPCResponse {
	return IPCResponse{OK: true, Data: `"resumed and delivered"`, Delivery: DeliveryDelivered}
}

// injectText sends text into a pane's PTY. Two modes based on the pane's
// noBracketedPaste config:
//
// Bracketed paste (default, for Claude Code): wraps text in ESC[200~/ESC[201~
// markers and writes directly to the PTY. Claude Code's parser handles this
// natively, collapsing large pastes (>800 chars) to references.
//
// Raw PTY write (noBracketedPaste=true, for OpenCode and generic agents):
// writes the message body directly to the PTY without bracketed paste markers.
//
// Codex is handled specially: current Codex enables bracketed paste in the TUI,
// while its non-bracketed paste-burst detector intentionally turns fast
// Enter-after-text sequences into a newline instead of submit. For Codex-like
// panes we therefore inject the body as direct bracketed paste bytes on the PTY
// and then submit separately.
//
// Safe to call from any goroutine.
//
// It returns the send's verdict (ini-69fy), each one decided where the path
// already decides it: nothing about when anything is written or pressed
// depends on the return.
func sendPaneTextLocked(pane *Pane, text string, enter bool) SendVerdict {
	if pane.ptmx == nil {
		// Never silent (ini-2s7q): the IPC handler refuses a stopped or
		// exited agent before reaching here, so a send arriving here was
		// routed past that check and its text goes nowhere.
		LogWarn("inject", "send dropped: pane has no PTY", "pane", pane.Name(), "text", logSnippet(text))
		return SendVerdict{DeliveryNotDelivered, noPTYVerdictReason}
	}

	// Modal guard (ini-7txh): never paste a body or fire a submit into an open
	// Claude Code modal (AskUserQuestion / permission prompt). An option picker
	// swallows the pasted body and reads the submit key as "confirm the
	// highlighted option" — auto-answering a destructive default the operator
	// never saw. Defer the message to the queue instead; readLoop re-delivers it
	// once the modal closes. This guards EVERY send caller (IPC) and is
	// reached after sendMu is held, so the check races minimally with
	// the paste it gates. The resume/modal-drain paths only inject when the modal
	// is gone, so they pass straight through; if a modal reopens mid-drain the
	// message is safely re-enqueued rather than lost.
	if paneHasModal(pane) {
		// A drain re-delivering mail it already holds (ini-1898): back to the
		// front with its original time, quietly. The "deferred" line below is
		// logged once per message, when it is first held.
		if m, ok := pane.takeRedelivery(text, enter); ok {
			pane.requeueFront(m)
			LogDebug("inject", "held message still held: dialog open", "pane", pane.Name(),
				"held_for", time.Since(m.Time).Round(time.Second))
			return SendVerdict{Outcome: DeliveryDeferred}
		}
		dropped := pane.enqueueHeld(text, enter)
		preview := text
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		// INFO, not Debug (ini-9gvn): the live incident left ZERO trace in a
		// 7.8MB fleet log because this was the only record and it was below the
		// default level. A message the fleet is holding is not debug detail.
		LogInfo("inject", "deferred: modal open", "pane", pane.Name(), "enter", enter,
			"queued", pane.QueuedMessageCount(), "queue_dropped", dropped)
		pane.reportEvent(AgentEvent{
			Type:   EventMessageSent,
			Pane:   pane.Name(),
			Detail: "deferred (modal open): " + preview,
		})
		return SendVerdict{Outcome: DeliveryDeferred}
	}

	// A withheld submit gets its fate before this send touches the composer.
	pane.settlePendingSubmit()

	useCodexBracketedPaste := pane.noBracketedPaste && pane.AgentType() == config.AgentTypeCodex
	codexQueueSubmit := codexShouldQueueSubmit(pane)
	mode := "bracketed"
	if pane.noBracketedPaste {
		mode = "raw"
	}
	if useCodexBracketedPaste {
		mode = "codex-bracketed"
	}
	LogDebug("inject", "send start", "pane", pane.Name(), "agent_type", pane.AgentType(), "mode", mode, "bytes", len(text), "enter", enter)
	// Stash any partially typed input before injecting so that the incoming
	// message doesn't corrupt text the user was composing (ini-gd0). Codex/raw
	// panes skip this because they inject directly to the PTY instead of through
	// the emulator keystroke path.
	//
	// ONLY WHEN WE ARE GOING TO SUBMIT (ini-a9d8). Claude restores stashed
	// text after a SUBMIT, so stashing on a send that never submits strands
	// what the operator typed: paste into a viewer window routes through here
	// with enter=false, and an operator who had typed "review this: " before
	// pasting would lose it. Today they lose the paste; inheriting the stash
	// unconditionally would have made the fix worse than the bug for them.
	// Safe by construction: no caller passed enter=false before a9d8, so this
	// condition changes no existing behaviour.
	stashed := false
	if !pane.noBracketedPaste && enter {
		pane.emu.SendKey(uv.KeyPressEvent(uv.Key{Code: 's', Mod: uv.ModCtrl}))
		time.Sleep(75 * time.Millisecond)
		stashed = true
	}

	// Belt baseline (ini-zjhg), sampled AFTER the stash because the stash is
	// itself a composer edit. hasComposer false means no prompt glyph anywhere
	// on screen -- a plain shell, a booting agent, a pane whose UI we do not
	// model -- and the belt stands down for those rather than blocking their
	// submits. That is a stated choice, not an oversight: every Claude dialog
	// this guards against is an option picker and therefore HAS a glyph, so a
	// glyph-free screen is not the dangerous state.
	//
	// SCOPED TO THE MEASURED FAMILY, chosen out loud rather than inherited. The
	// echo behaviour the belt depends on was measured on Claude Code's
	// bracketed-paste path and nowhere else. Codex, OpenCode and generic raw
	// panes were NOT measured, and a precondition on unmeasured rendering is
	// how a safety check becomes a delivery regression -- the specific harm AC
	// item 2 of ini-zjhg forbids. Those families keep layer 1 (which covers
	// codex-native confirm dialogs via the "enter to confirm" pattern) until
	// someone measures their composers and widens this deliberately.
	beltApplies := !pane.noBracketedPaste
	tailBefore, hasComposer := composerTail(pane)

	if useCodexBracketedPaste {
		var buf []byte
		buf = append(buf, "\x1b[200~"...)
		buf = append(buf, text...)
		buf = append(buf, "\x1b[201~"...)
		n, err := pane.ptmx.Write(buf)
		LogDebug("inject", "body written", "pane", pane.Name(), "mode", mode, "bytes", n, "err", err)
	} else if pane.noBracketedPaste {
		// Direct PTY write for text bytes, but route Enter through the emulator.
		// The raw text path avoids the emulator's key-to-byte translation for the
		// message body, which Codex can misinterpret. For the final submit key we
		// deliberately use the emulator path so Enter encoding stays aligned with
		// the PTY/terminal mode the emulator is already managing.
		var buf []byte
		buf = append(buf, text...)
		n, err := pane.ptmx.Write(buf)
		LogDebug("inject", "body written", "pane", pane.Name(), "mode", mode, "bytes", n, "err", err)
	} else {
		// Bracketed paste: ESC[200~ + text + ESC[201~ directly to PTY.
		var buf []byte
		buf = append(buf, "\x1b[200~"...)
		buf = append(buf, text...)
		buf = append(buf, "\x1b[201~"...)
		n, err := pane.ptmx.Write(buf)
		LogDebug("inject", "body written", "pane", pane.Name(), "mode", mode, "bytes", n, "err", err)
	}
	// The delivery probe's "written" stamp (ini-pqdy.4) belongs HERE, at the
	// body's PTY write, not after SendText returns: SendText goes on to wait
	// before the submit, and the child echoes the body as soon as it lands.
	notePerfMarkerWritten(pane, text)

	if !enter {
		LogInfo("inject", "typed, no submit requested", "pane", pane.Name(), "mode", mode, "bytes", len(text))
		return SendVerdict{Outcome: DeliveryDelivered}
	}

	// NEVER-SUBMIT BELT (ini-zjhg), layer 2 of two. Layer 1 (the modal guard
	// above) decided this pane had no dialog; if it was wrong, the body has just
	// been swallowed by a picker and the submit key below would ANSWER it. So
	// the submit is conditional on positive evidence that the body reached a
	// composer, which is evidence layer 1 never consults.
	//
	// The constraint this must not violate: a CORRECT drain still delivers a
	// complete, actionable message. Text sitting unsubmitted in a composer is
	// delayed delivery, not delivery. That is why the belt is a positive check
	// on our own echo rather than a blanket withholding of the terminator --
	// in the normal case it passes and the submit goes out unchanged.
	if beltApplies && hasComposer {
		if accepted, lastTail := composerAcceptedBody(pane, tailBefore, newPendingSubmit(text, mode, "")); !accepted {
			withholdSubmit(pane, text, mode, tailBefore, lastTail)
			return SendVerdict{DeliveryUnconfirmed, withheldVerdictReason}
		}
	}

	if useCodexBracketedPaste {
		time.Sleep(codexBracketedSubmitDelay)
		method := sendCodexSubmit(pane, codexQueueSubmit)
		LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", method, "delay_ms", codexBracketedSubmitDelay.Milliseconds())
		LogInfo("inject", "submit sent, unconfirmed", "pane", pane.Name(), "mode", mode, "why", "no composer check for this agent type")
		if pane.IsAlive() && !stashed {
			// Retry only when no stash was active. After a stash, Claude Code
			// auto-restores the stashed text into the prompt, making
			// promptHasContent a false positive (ini-vxw).
			time.Sleep(bracketedPasteSubmitDelay)
			if promptHasContent(pane) {
				method = sendCodexSubmit(pane, codexQueueSubmit)
				LogDebug("inject", "submit retry", "pane", pane.Name(), "mode", mode, "method", method)
			}
		}
		return SendVerdict{DeliveryUnchecked, uncheckedVerdictReason}
	}

	if pane.noBracketedPaste {
		// Let Codex's non-bracketed paste-burst detection expire before sending
		// Enter. The 8ms parser window observed in source was not enough in the
		// full PTY/TUI pipeline; use a larger margin here.
		time.Sleep(rawSubmitDelay)
		LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", "pty-enter", "delay_ms", rawSubmitDelay.Milliseconds())
		LogInfo("inject", "submit sent, unconfirmed", "pane", pane.Name(), "mode", mode, "why", "no composer check for this agent type")
		if config.IsCodexLikeAgentType(pane.AgentType()) && pane.submitKey != "ctrl+enter" {
			_, _ = pane.ptmx.Write([]byte("\r"))
			return SendVerdict{DeliveryUnchecked, uncheckedVerdictReason}
		}
		sendSubmitKey(pane.emu, pane.submitKey)
		return SendVerdict{DeliveryUnchecked, uncheckedVerdictReason}
	}

	// Bracketed paste mode: wait for Claude Code's paste completion
	// (100ms PASTE_COMPLETION_TIMEOUT_MS) plus async rendering time. The
	// sighting looks for our body in the box during this wait, on its own
	// goroutine; it is joined only after the Enter, so it cannot move or gate
	// it (ini-pkin).
	sighting := startPreEnterSighting(pane, newPendingSubmit(text, mode, ""))
	time.Sleep(bracketedPasteSubmitDelay)
	LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", "emulator", "delay_ms", bracketedPasteSubmitDelay.Milliseconds())
	sendSubmitKey(pane.emu, pane.submitKey)
	ps := newPendingSubmit(text, mode, "")
	ps.seenInBox = sighting.wait()
	ps.census = sighting.census
	return confirmAndRetrySubmit(pane, ps)
}

// submitConfirmWindow is how long the send path watches the composer after
// the Enter for OUR body to leave it (ini-nma5). Claude clears a taken
// submit within a frame or two; the window is generous for a loaded host.
// Two windows (the Enter, then one resend) keep a second miss surfaced
// within ~2s (AC 2). A variable so tests can shorten it.
var submitConfirmWindow = 1000 * time.Millisecond

// submitSampleMidpoint, when set, runs inside each confirmation sample
// between the ❯-row read and the box read. Nil in production. Tests use it to
// land a repaint exactly there, the tear the stable-sample check exists for,
// which timing alone produces only by luck.
var submitSampleMidpoint func()

// submitOutcome is what confirmSubmit saw.
type submitOutcome int

const (
	submitTaken submitOutcome = iota
	submitNotTaken
	submitUnknown
)

// confirmSubmit watches the composer after the Enter and logs ONE Info
// outcome line, so how often a submit is lost becomes readable from the
// operator's own log. It reports the outcome: taken, not taken (our body is
// provably still in the composer), or unknown.
//
// TAKEN IS A TRANSITION (ini-k3g2): our body must first be SEEN in the box,
// then a later sample must show a composer that no longer holds it.
// Confirming on a sample that could not prove our text was ever there counted
// a box that never held it as a delivery: measured on a 52-agent fleet, 253
// of 1,613 sends confirmed at 0 ms and the retry almost never ran.
//
// THE SIGHTING MAY COME BEFORE THE ENTER (ini-pkin). ps.seenInBox carries a
// sighting from the wait before the Enter (startPreEnterSighting), so the
// first stable sample after it that shows a composer without our body
// completes the transition, possibly at 0 ms. Without it, a busy agent lost
// its one post-Enter sample of our text to a torn frame and Claude had
// cleared the box by the next: measured, 325 of 1,413 sends ended unknown
// and every one checked had been delivered.
//
// OUR BODY, NOT ANY TEXT: after a submit, Claude restores the operator's
// stashed draft into the composer, so "the composer has content" is not
// evidence the Enter was lost. The pre-Enter acceptance check (the box
// changed) is not consulted: it is not a sighting of our text.
func confirmSubmit(pane *Pane, ps *pendingSubmit, sentAt time.Time) submitOutcome {
	outcome, _ := watchSubmit(pane, ps, sentAt)
	return outcome
}

// watchSubmit is confirmSubmit with the reason an UNKNOWN outcome gives the
// operator.
func watchSubmit(pane *Pane, ps *pendingSubmit, sentAt time.Time) (submitOutcome, string) {
	deadline := sentAt.Add(submitConfirmWindow)
	seen, sawComposer := ps.seenInBox, false
	var tail, mismatch string
	var found, held bool
	for {
		sTail, sFound, sHeld, stable := submitSample(pane, ps)
		ps.census.after++
		ps.census.count(sHeld, stable)
		if stable {
			tail, found, held = sTail, sFound, sHeld
			if found {
				sawComposer = true
			}
			if !held && found && mismatch == "" {
				mismatch = placeholderMismatch(tail, ps)
			}
			if held {
				seen = true
			} else if seen && found {
				LogInfo("inject", "submitted: composer cleared", "pane", pane.Name(), "mode", ps.mode,
					"ms", time.Since(sentAt).Milliseconds(), "text", ps.preview,
					"composer", composerSnapshot(pane))
				return submitTaken, ""
			}
		}
		if time.Now().After(deadline) {
			if held {
				LogInfo("inject", "SUBMIT NOT TAKEN: composer still holds our text", "pane", pane.Name(),
					"mode", ps.mode, "waited_ms", time.Since(sentAt).Milliseconds(), "text", ps.preview,
					"composer", composerSnapshot(pane))
				return submitNotTaken, ""
			}
			why := unconfirmedReason(mismatch, sawComposer, seen)
			LogInfo("inject", "submit outcome UNKNOWN: could not confirm", "pane", pane.Name(),
				"mode", ps.mode, "reason", why, "waited_ms", time.Since(sentAt).Milliseconds(),
				"text", ps.preview, "composer", composerSnapshot(pane),
				"samples_before", ps.census.before, "samples_after", ps.census.after,
				"torn", ps.census.torn, "stable", ps.census.stable, "sighted", ps.census.sighted)
			return submitUnknown, why
		}
		time.Sleep(composerEchoPoll)
	}
}

// submitSample reads the box once: the composer tail, whether a composer was
// found, whether it shows OUR body (submitSighted), and whether the frame was
// stable.
//
// ONE FRAME PER SAMPLE. The reads below each copy the screen row by row, and a
// repaint landing between them assembles a sample from two frames: the ❯ row
// from before, the box from after. Measured in a fake 1 run in 5 confirmed
// "taken" from such a tear while the screen was changing to one with no
// composer at all. A sample counts only when the composer rows read the same,
// at the same place, before and after it; otherwise it is skipped, neither
// seen nor gone.
//
// THE COMPOSER ROWS, NOT THE SCREEN (ini-s1kd). The sample's evidence lives
// in the box (composerSpan), and a tear that matters changes the box. A busy
// agent repaints its answer, spinner and status rows between any two reads,
// and comparing the whole screen tore every sample of a streaming pane, before
// the Enter as well as after: measured, 95 of 100 "could not confirm" in one
// loaded morning read "our text was never seen in the box", and not one of
// the 305 sends that morning was found still in a box.
//
// A DIALOG IS NOT A COMPOSER (ini-pkin). A picker's ❯ row reads like one, and
// once a sighting is carried from before the Enter, "a composer without our
// body" confirms. A dialog that opened during the wait takes the Enter while
// our body sits under it, unsent; that must never read as the box clearing.
func submitSample(pane *Pane, ps *pendingSubmit) (tail string, found, held, stable bool) {
	before := emuRowsBlocking(pane.emu)
	tail, found = composerTail(pane)
	found = found && !paneHasModal(pane)
	if submitSampleMidpoint != nil {
		submitSampleMidpoint()
	}
	held = submitSighted(pane, tail, found, ps)
	return tail, found, held, sameComposerSpan(before, emuRowsBlocking(pane.emu))
}

// sameComposerSpan reports whether two reads of the screen show the same
// composer rows at the same place. A box that moved counts as changed: the
// sample's inner reads index rows, and a moved box may have read rows of two
// frames.
func sameComposerSpan(a, b []string) bool {
	topA, spanA := composerSpan(a)
	topB, spanB := composerSpan(b)
	return topA == topB && slices.Equal(spanA, spanB)
}

// composerSpan is where a sample's evidence sits on a screen: the prompt row
// composerTailFromRows finds, through the first rule below it (Claude's bottom
// border), or through the last row when none follows; or, when the prompt row
// has scrolled off, the rows above the last rule up to the rule before it. top
// is the first row's index, -1 when the screen has neither.
//
// One rule test for both, ruleBoundedRegion's (isRegionRule), not the
// full-width isComposerRule: a pane narrower than its 40 runes draws a shorter
// border, and a span that ran past it would take in the status row and tear
// on its repaints. Scanning down from the prompt row, only our own body can
// sit above the border; a dash line in it ends the span early, and the prompt
// row, which changes whenever the box clears, is always in it.
func composerSpan(rows []string) (top int, span []string) {
	if _, row, found := composerTailFromRows(rows); found {
		end := len(rows)
		for r := row + 1; r < len(rows); r++ {
			if isRegionRule(rows[r]) {
				end = r + 1
				break
			}
		}
		return row, rows[row:end]
	}
	bottom := -1
	for r := len(rows) - 1; r >= 0; r-- {
		if isRegionRule(rows[r]) {
			bottom = r
			break
		}
	}
	if bottom <= 0 {
		return -1, nil
	}
	top = 0
	for r := bottom - 1; r >= 0; r-- {
		if isRegionRule(rows[r]) {
			top = r + 1
			break
		}
	}
	return top, rows[top : bottom+1]
}

// isRegionRule is ruleBoundedRegion's rule test: ten or more box-drawing
// dashes and nothing else.
func isRegionRule(s string) bool {
	t := strings.TrimSpace(s)
	return utf8.RuneCountInString(t) >= 10 && strings.Trim(t, "\u2500") == ""
}

// submitCensus counts what one send's samples saw (ini-s1kd), for the
// UNKNOWN line. Reporting only: it decides nothing.
type submitCensus struct {
	before, after int // samples taken before and after the Enter
	torn          int // samples whose composer rows changed while they were read
	stable        int // samples whose composer rows held still
	sighted       int // stable samples that showed our text
}

// count records one sample's verdict.
func (c *submitCensus) count(held, stable bool) {
	if !stable {
		c.torn++
		return
	}
	c.stable++
	if held {
		c.sighted++
	}
}

// preEnterSighting looks for OUR body in the box during the wait before the
// Enter (ini-pkin). The body sits there untouched for the whole wait, which
// is the one time a busy agent reliably shows it: after the Enter Claude
// clears it within about one sample.
type preEnterSighting struct {
	stop   chan struct{}
	done   chan struct{}
	seen   bool         // written by the sampler before done closes
	census submitCensus // likewise; read only after wait
}

// startPreEnterSighting samples the box every composerEchoPoll until one
// stable sample holds our body, with exactly the evidence the watch after the
// Enter uses (submitSample). A box that merely changed is not a sighting.
func startPreEnterSighting(pane *Pane, ps *pendingSubmit) *preEnterSighting {
	s := &preEnterSighting{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			_, _, held, stable := submitSample(pane, ps)
			s.census.before++
			s.census.count(held, stable)
			if held && stable {
				s.seen = true
				return
			}
			select {
			case <-s.stop:
				return
			case <-time.After(composerEchoPoll):
			}
		}
	}()
	return s
}

// wait stops the sampler and reports whether it saw our body. Called after
// the Enter is written, so the Enter never waits on a screen read.
func (s *preEnterSighting) wait() bool {
	close(s.stop)
	<-s.done
	return s.seen
}

// submitSighted reports whether this sample positively shows OUR body in the
// box: its own text (in the composer block, or in the rule-bounded region when
// the ❯ row has scrolled off), the short-body whole match, or Claude's
// collapsed placeholder with our exact line count.
func submitSighted(p *Pane, tail string, found bool, ps *pendingSubmit) bool {
	return (found && submitStillHeld(p, tail, ps)) || bodyTextVisible(p, ps) || boxRunIsOurs(p, ps)
}

// placeholderMismatch describes a collapsed placeholder in the box that is
// not ours by its count, or "" when the tail holds none.
func placeholderMismatch(tail string, ps *pendingSubmit) string {
	m := collapsedPaste.FindStringSubmatch(tail)
	if m == nil {
		return ""
	}
	return "the box showed a collapsed paste of +" + m[1] + " lines; ours would read +" +
		strconv.Itoa(ps.pasteCount) + ", a line count that does not match"
}

// unconfirmedReason says, for the operator, why a submit could not be
// confirmed either way. mismatch is the first placeholder of another count
// seen during the window: Claude may have taken it and cleared the box by the
// end, and it is still the explanation.
func unconfirmedReason(mismatch string, sawComposer, seen bool) string {
	switch {
	case seen:
		return "our text left the box but no composer was visible afterwards"
	case mismatch != "":
		return mismatch
	case !sawComposer:
		return "no composer visible"
	}
	return "our text was never seen in the box"
}

// submitRetryDialogReason is the could-not-confirm reason when the one resend
// is withheld because a dialog is open (ini-ytxc).
const submitRetryDialogReason = "dialog open at the retry"

// confirmAndRetrySubmit watches the Enter land; if the composer provably
// still holds OUR body it resends Enter ONCE, and a second miss is surfaced
// as NOT delivered, within about two seconds (ini-nma5 AC 2). An unknown
// outcome presses nothing (a blind Enter could answer a dialog) and is
// surfaced as "could not confirm" (ini-k3g2): never counted as taken.
//
// It returns what it surfaced, for a sender waiting on the reply (ini-69fy):
// the same outcome and reason as the toast, decided at the same moment.
func confirmAndRetrySubmit(pane *Pane, ps *pendingSubmit) SendVerdict {
	switch outcome, why := watchSubmit(pane, ps, time.Now()); outcome {
	case submitTaken:
		return SendVerdict{Outcome: DeliveryDelivered}
	case submitUnknown:
		pane.emitUnconfirmedSubmit(ps, why, time.Since(ps.withheldAt).Round(time.Second))
		return SendVerdict{DeliveryUnconfirmed, why}
	}
	// NEVER INTO A DIALOG (ini-ytxc), the guard deliverWithheldSubmit has:
	// the verdict above rests on a sample up to one poll old, and a
	// permission prompt that opened since would take this Enter and select
	// its highlighted option. Checked at the moment of pressing; under a
	// dialog nothing is pressed and the sender is told, never counted taken.
	if paneHasModal(pane) {
		LogInfo("inject", "submit retry withheld: dialog open", "pane", pane.Name(), "mode", ps.mode, "text", ps.preview)
		pane.emitUnconfirmedSubmit(ps, submitRetryDialogReason, time.Since(ps.withheldAt).Round(time.Second))
		return SendVerdict{DeliveryUnconfirmed, submitRetryDialogReason}
	}
	LogInfo("inject", "submit retry: Enter not taken, resending once", "pane", pane.Name(), "mode", ps.mode, "text", ps.preview)
	sendSubmitKey(pane.emu, pane.submitKey)
	switch outcome, why := watchSubmit(pane, ps, time.Now()); outcome {
	case submitNotTaken:
		pane.emitUndeliveredSubmit(ps, retryMissVerdictReason, time.Since(ps.withheldAt).Round(time.Second))
		return SendVerdict{DeliveryNotDelivered, retryMissVerdictReason}
	case submitUnknown:
		pane.emitUnconfirmedSubmit(ps, why, time.Since(ps.withheldAt).Round(time.Second))
		return SendVerdict{DeliveryUnconfirmed, why}
	}
	return SendVerdict{Outcome: DeliveryDelivered}
}

// submitStillHeld reports whether the composer still shows our body after an
// Enter. The belt's proof, plus a short-body form: under minProvenRunes the
// rune proof can never fire, so a short message is held when its whole
// whitespace-stripped body is in the composer block.
func submitStillHeld(p *Pane, tail string, ps *pendingSubmit) bool {
	if composerHoldsOurBody(p, tail, ps) {
		return true
	}
	body := stripSpace(strings.Join(ps.bodyLines, ""))
	if body == "" || len([]rune(body)) >= minProvenRunes {
		return false
	}
	rows, ok := composerBlock(p)
	return ok && strings.Contains(stripSpace(strings.Join(rows, "")), body)
}

// bodyTextVisible reports whether OUR body's own text is in the composer
// block: the rune proof, or for a body under the proof floor the whole-body
// match. Not the collapsed placeholder, which needs the transition to mean
// anything (composerProvesOurPaste).
func bodyTextVisible(p *Pane, ps *pendingSubmit) bool {
	rows, ok := composerRegion(p)
	if !ok {
		return false
	}
	// Rune proof only. No short-body form: unlike submitStillHeld, a wrong
	// yes here presses Enter, and a short body is text the operator could
	// have typed.
	return provenRunes(rows, ps.bodyLines) >= minProvenRunes
}

// boxRunIsOurs reports whether everything the composer box shows is a run of
// OUR body (ini-lvxw). Claude Code 2.1.310 caps the box at a few rows and
// scrolls a long message inside it, keeping the end in view: in a narrow pane
// the box starts mid-message, the start of our text is never on screen, and
// provenRunes, which proves a body line only when the whole line is visible,
// proves nothing. Here the box's whole visible text, the rows between its
// two rules with the prompt glyph dropped, must appear contiguously in our
// space-stripped body, and be at least minProvenRunes long. Any text in the
// box that is not ours, and an empty box, prove nothing. Never under a
// dialog, like the rule-bounded read it uses. VERDICT ONLY: submitSighted
// reads it, so it decides taken or held after an Enter and the sighting
// before it; nothing that decides whether to press Enter reads it.
//
// A BOX THAT RUNS OFF THE BOTTOM EDGE (ini-jpal). In a short pane (a cell of
// a full grid, about 10 rows) the box's bottom rule and the footer fall below
// the pane's last row, so there is no second rule; the box is then the rows
// from its top rule to the last row (openBottomBox), read by the same rule.
// Measured: 20 of 20 long sends read "never seen" in a 10-row pane, all of
// them landed.
func boxRunIsOurs(p *Pane, ps *pendingSubmit) bool {
	if paneHasModal(p) {
		return false
	}
	screen := emuRowsBlocking(p.emu)
	rows, ok := openBottomBox(screen)
	if !ok {
		rows, ok = ruleBoundedRegion(screen)
	}
	if !ok {
		return false
	}
	run := strings.TrimPrefix(stripSpace(strings.Join(rows, "")), "\u276f")
	if len([]rune(run)) < minProvenRunes {
		return false
	}
	return strings.Contains(stripSpace(strings.Join(ps.bodyLines, "")), run)
}

// openBottomBox returns a box whose bottom rule is off the screen: the rows
// after the last rule on screen, through the last row, when the first of them
// is the prompt row. A box whose bottom rule is on screen has
// the footer, not a prompt row, after its last rule, so it is not one.
func openBottomBox(rows []string) ([]string, bool) {
	last := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if isRegionRule(rows[i]) {
			last = i
			break
		}
	}
	if last < 0 || last+1 >= len(rows) || !strings.HasPrefix(strings.TrimSpace(rows[last+1]), "\u276f") {
		return nil, false
	}
	var box []string
	for _, r := range rows[last+1:] {
		box = append(box, strings.TrimSpace(r))
	}
	return box, true
}

// composerRegion is the composer block, or -- when Claude's ❯ row has
// scrolled off the top of a short pane under a long wrapped body -- the rows
// above Claude's bottom rule (ini-cg8y). Measured: in a 10-row pane a
// one-paragraph message wrapped to six rows pushed the ❯ row off screen, the
// finder saw no composer, and the belt withheld a body sitting in plain
// view. Used ONLY for positive evidence that our own text is there; the
// tail comparison still needs the ❯ row. Never while a dialog is up: a
// dialog quoting our text above a rule must not read as our composer.
func composerRegion(p *Pane) ([]string, bool) {
	if rows, ok := composerBlock(p); ok {
		return rows, true
	}
	if paneHasModal(p) {
		return nil, false
	}
	return ruleBoundedRegion(emuRowsBlocking(p.emu))
}

// ruleBoundedRegion returns the rows between the last full-width rule on
// screen (Claude's composer bottom border) and the rule before it, or the
// top of the screen when that one has scrolled off.
func ruleBoundedRegion(rows []string) ([]string, bool) {
	isRule := func(s string) bool {
		t := strings.TrimSpace(s)
		return utf8.RuneCountInString(t) >= 10 && strings.Trim(t, "\u2500") == ""
	}
	bottom := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if isRule(rows[i]) {
			bottom = i
			break
		}
	}
	if bottom <= 0 {
		return nil, false
	}
	top := 0
	for i := bottom - 1; i >= 0; i-- {
		if isRule(rows[i]) {
			top = i + 1
			break
		}
	}
	var region []string
	for _, r := range rows[top:bottom] {
		region = append(region, strings.TrimSpace(r))
	}
	return region, len(region) > 0
}

// composerSnapshot is the composer block for a log line, bounded.
func composerSnapshot(p *Pane) string {
	rows, ok := composerBlock(p)
	if !ok {
		return "(no composer)"
	}
	return logSnippet(strings.Join(rows, " | "))
}

// logSnippet bounds composer text written to the log to the preview's
// length (60), so a log line never carries more of a message than the
// preview field already does (ini-cg8y).
func logSnippet(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 60 {
		return string(r[:57]) + "..."
	}
	return s
}

func (t *TUI) injectText(pane *Pane, text string, enter bool) {
	pane.mu.Lock()
	pane.lastMessageReceived = time.Now()
	pane.mu.Unlock()
	waitForCodexReadyIfNeeded(pane)
	pane.sendMu.Lock()
	defer pane.sendMu.Unlock()
	sendPaneTextLocked(pane, text, enter)
}

// waitForCodexReadyIfNeeded polls for the Codex/OpenCode ready prompt
// BEFORE acquiring sendMu. This prevents the up-to-10s poll from blocking
// concurrent sends on other goroutines that share the same pane mutex.
func waitForCodexReadyIfNeeded(pane *Pane) {
	if pane.ptmx == nil {
		return
	}
	codexQueueSubmit := codexShouldQueueSubmit(pane)
	if config.IsCodexLikeAgentType(pane.AgentType()) && !codexQueueSubmit {
		ready := pane.waitForCodexReady(codexReadyTimeout)
		LogDebug("inject", "codex ready wait", "pane", pane.Name(), "ready", ready, "timeout_ms", codexReadyTimeout.Milliseconds())
	} else if codexQueueSubmit {
		LogDebug("inject", "codex queueing while running", "pane", pane.Name())
	}
}

// promptHasContent checks if the last ❯ prompt line has non-whitespace content.
// Used as a lightweight retry signal: if content remains after Enter, it was
// likely swallowed during paste processing.
func promptHasContent(p *Pane) bool {
	tail, found := composerTail(p)
	return found && tail != ""
}

// composerTail returns the text after the pane's lowest prompt glyph, and
// whether such a glyph exists at all.
//
// MEASURED CAVEAT, and it is the reason the never-submit belt is a DELTA on
// this value rather than a test of it (ini-zjhg, real Claude 2.1.232): a glyph
// on screen does not mean a composer. Claude's option pickers use the same \u276f as
// their SELECTION CURSOR, so an open trust prompt returns tail
// "1. Yes, I trust this folder" -- non-empty, and promptHasContent answers true
// with no composer anywhere on screen. Anything that reads this as "the app is
// accepting typed input" is reading a dialog as a prompt.
//
// What DOES discriminate, from the same capture: whether the tail CHANGES when
// we write a body. Composer, small body: "" -> "zjhg-small-body". Composer,
// 2KB body: "" -> "[Pasted text #1 +39 lines]" (Claude collapses large pastes,
// so matching the body text itself would false-negative exactly the big
// messages agents send). Open dialog: unchanged, and nothing on screen moved --
// the picker swallowed the paste silently.
func composerTail(p *Pane) (string, bool) {
	text, _, found := composerTailAt(p)
	return text, found
}

// composerTailAt is composerTail plus the row the prompt was found on, which
// the block reader needs and the tail readers do not.
func composerTailAt(p *Pane) (string, int, bool) {
	// emuRows copies each row under a lock, so a torn read here cannot flip
	// the submit decision (ini-wizq). This BLOCKS on the emulator; the main
	// loop uses composerTailFromRows over a tryScreenRows snapshot instead.
	return composerTailFromRows(emuRowsBlocking(p.emu))
}

// composerTailFromRows is composerTailAt over an already-read screen.
//
// CLAUDE'S ❯ WINS when the screen has one (ini-cg8y). Claude draws hint rows
// BELOW its composer with the › glyph -- "› stashed" whenever its stash
// holds something -- and a lowest-glyph-of-any-kind rule read that hint as
// the composer. Measured: once a withheld body was stashed, every later send
// compared "stashed" to "stashed", was withheld, and the agent received
// nothing. › and > stay as fallbacks for screens with no ❯ at all (Codex's
// own prompt is ›).
func composerTailFromRows(rows []string) (string, int, bool) {
	for _, glyphs := range [][]string{{"\u276f"}, {"\u203a", ">"}} {
		for row := len(rows) - 1; row >= 0; row-- {
			text := rows[row]
			for _, prompt := range glyphs {
				if idx := strings.LastIndex(text, prompt); idx >= 0 {
					return strings.TrimSpace(text[idx+len(prompt):]), row, true
				}
			}
		}
	}
	return "", 0, false
}

// composerEchoWindow bounds how long the belt waits for the body to appear in
// the composer before concluding it was swallowed. Measured body-to-composer
// latency was under one poll interval on real Claude; the window is generous
// against a slow render because the cost of being wrong is asymmetric -- too
// short withholds a legitimate submit (delayed delivery), too long only delays
// the submit of a message that was going to be submitted anyway.
const composerEchoWindow = 1500 * time.Millisecond

// beltEchoWindow is composerEchoWindow stretched by the main loop's lag
// (ini-cg8y): the window was measured on a light loop, and a loop missing
// dozens of ticks a minute delivers the echo late. One extra window per 30
// missed ticks in the last minute, capped at 3x.
func beltEchoWindow() time.Duration {
	missed := loopMissedLastMinute.Load()
	scale := 1 + float64(missed)/30
	if scale > 3 {
		scale = 3
	}
	return time.Duration(float64(composerEchoWindow) * scale)
}

// composerEchoPoll is the belt's sampling interval within that window.
const composerEchoPoll = 50 * time.Millisecond

// composerAcceptedBody reports whether the body we just wrote visibly landed in
// a composer -- the never-submit belt of ini-zjhg.
//
// It deliberately asks a question the modal guard never asks. The guard asks
// "is a dialog present?", from the application's declaration and from the
// screen; this asks "did MY OWN text land somewhere that accepts text?". The
// two fail under different conditions, which is the entire point of having
// both: a dialog that is open but scrolled out defeats the screen term of the
// guard and is caught here, because a picker swallows the paste and the
// composer never changes. Neither layer can see the other's failure.
//
// A false answer here costs a stray unsubmitted line. A false answer the other
// way costs an operator's question answered by a machine.
func composerAcceptedBody(p *Pane, before string, ps *pendingSubmit) (accepted bool, lastTail string) {
	deadline := time.Now().Add(beltEchoWindow())
	lastTail = "(no composer)"
	for {
		// A vanished glyph counts as NOT accepted: the comparison is only
		// meaningful against a composer that is still there to have accepted it.
		after, found := composerTail(p)
		if found {
			lastTail = after
		}
		// Two kinds of evidence, either sufficient: the ❯ row changed, or OUR
		// text is provably in the composer region (ini-cg8y) -- including a
		// composer whose ❯ row scrolled off a short pane. A picker swallows
		// the paste, so neither holds for one: the zjhg safety is untouched.
		if (found && after != before) || bodyTextVisible(p, ps) {
			return true, lastTail
		}
		if time.Now().After(deadline) {
			return false, lastTail
		}
		time.Sleep(composerEchoPoll)
	}
}

// withholdSubmit records a submit the belt refused to send, and ARMS a retry
// for it (ini-vpwg). Before this, withholding was the end of the story: the
// body sat in the composer, the submit never went, and nothing re-delivered
// it -- a message the fleet was holding with no holder.
func withholdSubmit(pane *Pane, text, mode, tailBefore, lastPolled string) {
	tail, found := composerTail(pane)
	ps := newPendingSubmit(text, mode, tail)
	pane.mu.Lock()
	pane.pendingSubmit = ps
	pane.mu.Unlock()

	// THE DEADLINE IS ENFORCED BY sweepWithheldSubmits, NOT FROM HERE, and
	// that is the whole point (ini-vpwg, finding 4, found by eng2 against
	// their own code). The retry hook runs on pane OUTPUT, so an agent that
	// withholds and then goes quiet -- crashed, wedged, or simply finished --
	// never re-enters it, never reaches its own deadline, and holds the
	// message forever: this bead's exact sin, reintroduced one level down by
	// the mechanism meant to fix it.
	//
	// A per-withhold time.AfterFunc would also fire without output and was
	// the first repair written here. The sweep replaced it because it guards
	// the CLASS rather than the instance: every pane holding a pendingSubmit
	// is bounded, however that field came to be set, so a future arming path
	// that forgets to start a clock is bounded anyway. A timer armed beside
	// each write is a rule someone has to remember; a sweep is a rule the
	// structure keeps.

	// INFO, not Debug (ini-vpwg, applying the rule ini-9gvn already bought).
	// The comment twenty lines up in this file states it for the modal-guard
	// path: a message the fleet is holding is not debug detail, because the
	// live 9gvn incident left ZERO trace in a 7.8MB fleet log when its only
	// record sat below the default level. THIS path holds a message in a worse
	// way than that one -- the body is already in the composer and nothing
	// re-delivers it -- and it was logging at Debug.
	// WHAT WAS COMPARED, so a withhold can be judged from the log (ini-cg8y):
	// body_in_block says whether OUR text was in fact anywhere in the composer
	// block at this moment -- true here is a FALSE withhold. Rows are bounded
	// to the preview's length: no more of the body than the log already has.
	LogInfo("inject", "submit WITHHELD: body never reached a composer", "pane", pane.Name(), "mode", mode,
		"tail_before", logSnippet(tailBefore), "last_polled", logSnippet(lastPolled),
		"composer", composerSnapshot(pane), "body_in_block", found && submitStillHeld(pane, tail, ps),
		"echo_window_ms", beltEchoWindow().Milliseconds(), "loop_missed_last_min", loopMissedLastMinute.Load())
	EmitEvent(pane.eventCh, AgentEvent{
		Type:   EventMessageSent,
		Pane:   pane.Name(),
		Detail: "submit withheld, awaiting composer repaint (body did not reach the composer; a dialog may be open): " + ps.preview,
	})
}

func sendCodexSubmit(pane *Pane, queue bool) string {
	if queue {
		_, _ = pane.ptmx.Write([]byte("\t"))
		return "pty-tab"
	}
	if pane.submitKey != "ctrl+enter" {
		_, _ = pane.ptmx.Write([]byte("\r"))
		return "pty-enter"
	}
	sendSubmitKey(pane.emu, pane.submitKey)
	return "emulator-ctrl+enter"
}
func (t *TUI) handleIPCPatrol(conn net.Conn, req IPCRequest) {
	lines := req.Lines
	if lines <= 0 {
		lines = 20
	}
	type patrolEntry struct {
		Name     string    `json:"name"`
		Activity string    `json:"activity"`
		Bead     string    `json:"bead,omitempty"`
		Alive    bool      `json:"alive"`
		Visible  bool      `json:"visible"`
		Content  string    `json:"content"`
		Busy     *PaneBusy `json:"busy,omitempty"`
	}
	var result []patrolEntry
	t.runOnMain(func() {
		result = make([]patrolEntry, len(t.panes))
		for i, p := range t.panes {
			result[i] = patrolEntry{
				Name:     p.Name(),
				Activity: p.Activity().String(),
				Bead:     p.BeadID(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[agentKey(p)],
				Content:  peekContent(p, lines),
				Busy:     paneBusy(p, time.Now()),
			}
		}
	})
	data, _ := json.Marshal(result)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

// peekContent extracts the last N lines of terminal content from a pane's
// emulator. Returns the content as a string with newline-separated lines.
// If lines <= 0, returns all non-blank content.
func peekContent(p PaneView, lines int) string {
	// Reached from the main loop (:peek, patrol under runOnMain), so it may
	// not wait on the pane (ini-psjt): a pane whose lock stays held for
	// peekTryBudget reports that instead of content. Callers on a goroutine
	// that MAY wait use peekReadBlocking.
	var r peekRead
	if !withEmulator(p.Emulator(), peekTryBudget, func(e *vt.Emulator) { r = readHistory(e, lines) }) {
		return peekUnreadable + "\n"
	}
	return r.text
}

// peekNotice says why a peek returned fewer lines than asked, for stderr
// (ini-jpal, condition from ini-ctrw): a full-screen program is read as its
// screen only; otherwise the pane's whole output is shorter than asked, and
// the line names how much history a pane keeps. "" when the reply holds what
// was asked, nothing was asked (0), or nothing could be read.
func peekNotice(r peekRead, lines int) string {
	if r.found >= lines || r.rows == 0 {
		return ""
	}
	if r.alt {
		return fmt.Sprintf("pane is %d rows; peek reads the screen only", r.rows)
	}
	return fmt.Sprintf("peek returned %d of %d lines: all the pane keeps (up to %d lines of history)", r.found, lines, r.kept)
}

// peekLastLines trims trailing blanks and keeps the last N rows -- the one
// formatting both peek forms share, so their output cannot diverge.
func peekLastLines(allLines []string, lines int) string {
	for i := range allLines {
		allLines[i] = strings.TrimRight(allLines[i], " ")
	}
	// Strip trailing blank lines.
	contentEnd := len(allLines)
	for contentEnd > 0 && allLines[contentEnd-1] == "" {
		contentEnd--
	}
	allLines = allLines[:contentEnd]

	// Return last N lines.
	if lines > 0 && lines < len(allLines) {
		allLines = allLines[len(allLines)-lines:]
	}

	var buf strings.Builder
	for _, line := range allLines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return buf.String()
}

func (t *TUI) handleIPCBead(conn net.Conn, req IPCRequest) {
	// One log line per call, refusals included, so a triage can tell from
	// initech.log whether a ribbon write (a clear above all) arrived.
	reply := func(resp IPCResponse) {
		result := "ok"
		if !resp.OK {
			result = "refused: " + resp.Error
		}
		LogInfo("ipc", "bead action", "target", req.Target, "beads", beadsForLog(req.Text), "result", result)
		writeIPCResponse(conn, resp)
	}
	if req.Target == "" {
		reply(IPCResponse{Error: "target is required (set INITECH_AGENT or use --agent)"})
		return
	}
	// Validate bead text before touching TUI state. Tab is allowed as the
	// id\ttitle delimiter; other control characters are rejected.
	if len(req.Text) > 256 {
		reply(IPCResponse{Error: "bead text too long (max 256 chars)"})
		return
	}
	for _, ch := range req.Text {
		if ch != '\t' && (ch < 0x20 || ch == 0x7F) {
			reply(IPCResponse{Error: "bead text contains control characters"})
			return
		}
	}
	var pv PaneView
	if !t.runOnMain(func() { pv = t.findPaneByName(req.Target) }) {
		reply(IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		reply(IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	if !pv.IsAlive() {
		// SUSPENDED IS NOT DEAD (ini-g7fl): the old message said "restart it",
		// which fights resume-on-message -- a restart discards the queue and
		// the suspension bookkeeping. Name the state and the designed remedy.
		if pv.IsSuspended() {
			reply(IPCResponse{Error: fmt.Sprintf(
				"pane %q is suspended; it resumes on message (initech send %s ...) and the bead can be set then",
				req.Target, req.Target)})
			return
		}
		// A dead pane's process has exited; silently "succeeding" here would
		// mean the bead display update is lost the moment the pane is
		// restarted (NewPane starts with no bead state), indistinguishable
		// from the request never having been sent (ini-cs7).
		reply(IPCResponse{Error: fmt.Sprintf("pane %q is not alive (agent process has exited); restart it before setting its bead", req.Target)})
		return
	}
	// Parse bead text. Supports:
	// - "id\ttitle" (tab-delimited: ID with title)
	// - "id1,id2,id3" (comma-separated: multiple IDs, no titles)
	// - "id" (plain: single ID, no title)
	// - "" (clear)
	text := req.Text
	if strings.Contains(text, ",") {
		ids := strings.Split(text, ",")
		for i := range ids {
			ids[i] = strings.TrimSpace(ids[i])
		}
		pv.SetBeads(ids)
	} else if idx := strings.IndexByte(text, '\t'); idx >= 0 {
		pv.SetBead(text[:idx], text[idx+1:])
	} else {
		pv.SetBead(text, "")
	}
	reply(IPCResponse{OK: true})
}

// beadsForLog renders a bead action's text for the log: "clear" for none,
// the ids without the title otherwise, cut short if long.
func beadsForLog(text string) string {
	if text == "" {
		return "clear"
	}
	if i := strings.IndexByte(text, '\t'); i >= 0 {
		text = text[:i]
	}
	if r := []rune(text); len(r) > 80 {
		text = string(r[:80]) + "..."
	}
	return text
}

// noBeadPane is a pane that can show the no-bead marker.
type noBeadPane interface {
	SetNoBeadWork(words string)
	NoBeadWork() string
}

// noBeadOf returns pv's no-bead marker words, or "" for a pane without one.
func noBeadOf(pv PaneView) string {
	if nb, ok := pv.(noBeadPane); ok {
		return nb.NoBeadWork()
	}
	return ""
}

// handleIPCNoBead marks a pane as working without a bead (initech assign
// with -m and no bead): its ribbon shows "no bead" and the words in Text
// until the next bead registration or clear. A separate action, not a form
// of "bead", so an older TUI answers "unknown action" and leaves the ribbon
// as it was instead of reading the words as a bead. Text is checked and the
// pane state refused exactly as the bead action does.
func (t *TUI) handleIPCNoBead(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeIPCResponse(conn, IPCResponse{Error: "no_bead needs the work's first words"})
		return
	}
	if len(req.Text) > 256 {
		writeIPCResponse(conn, IPCResponse{Error: "no_bead text too long (max 256 chars)"})
		return
	}
	for _, ch := range req.Text {
		if ch < 0x20 || ch == 0x7F {
			writeIPCResponse(conn, IPCResponse{Error: "no_bead text contains control characters"})
			return
		}
	}
	var pv PaneView
	if !t.runOnMain(func() { pv = t.findPaneByName(req.Target) }) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	if !pv.IsAlive() {
		if pv.IsSuspended() {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf(
				"pane %q is suspended; it resumes on message (initech send %s ...) and the bead can be set then",
				req.Target, req.Target)})
			return
		}
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q is not alive (agent process has exited); restart it before setting its bead", req.Target)})
		return
	}
	nb, ok := pv.(noBeadPane)
	if !ok {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q cannot show a no-bead marker", req.Target)})
		return
	}
	nb.SetNoBeadWork(req.Text)
	writeIPCResponse(conn, IPCResponse{OK: true})
}

func (t *TUI) findPane(name string) *Pane {
	for _, p := range t.panes {
		if p.Name() == name {
			if lp, ok := p.(*Pane); ok {
				return lp
			}
		}
	}
	return nil
}

func (t *TUI) handleIPCQuit(conn net.Conn) {
	writeIPCResponse(conn, IPCResponse{OK: true})
	t.quitOnce.Do(func() { close(t.quitCh) })
}

// ipcAction is a closure dispatched to the TUI main event loop for safe
// access to unsynchronised TUI state (primarily t.panes). IPC goroutines
// must not read or write t.panes directly; all such access goes through
// runOnMain to serialise with the render loop.
type ipcAction struct {
	fn   func()
	done chan struct{}
}

// currentGoroutineID returns the ID of the calling goroutine, parsed from the
// "goroutine N [state]:" header runtime.Stack always writes first. Go does
// not expose goroutine IDs as a supported API; this is the standard, if
// inelegant, way to answer "is this the goroutine I think it is" without
// threading a token through every runOnMain call site (present and future).
// Used only by runOnMain's reentrancy check below, never on a per-frame or
// per-byte hot path, so the cost of parsing a short stack trace is immaterial.
func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i >= 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}

// runOnMain dispatches fn to the TUI main event loop and blocks until it
// executes. Returns false if the TUI is shutting down (quitCh was closed).
// When ipcCh is nil (test contexts without a running event loop) fn is
// executed directly on the calling goroutine.
//
// Reentrancy guard (ini-jesh): if the CALLER is already the TUI's own main
// goroutine — a command-bar handler like cmdAdd running inline as part of
// Run()'s own synchronous key handling, rather than an IPC connection or
// background goroutine — enqueueing onto ipcCh would deadlock forever: the
// only goroutine that ever drains ipcCh is this one, and it would be
// blocked waiting on itself. fn is executed directly in that case, which is
// exactly what the main loop would have done had the op reached its select
// statement, just without the pointless (and here, fatal) channel round trip.
// mainGoroutineID is captured once at TUI construction, before any other
// goroutine starts, so this comparison needs no additional synchronization.
// Tests that never set mainGoroutineID (the zero value) are unaffected —
// this guard cannot fire for them, so they exercise the same channel-based
// path they always have.
//
// Two-phase select: first, race the send against quit; second, race the
// completion signal against quit. This ensures quitCh always wins even when
// ipcCh has buffer space.
func (t *TUI) runOnMain(fn func()) bool {
	if t.ipcCh == nil {
		LogInfo("runOnMain", "ipcCh is nil, executing directly on caller goroutine")
		fn()
		return true
	}
	if t.mainGoroutineID != 0 && currentGoroutineID() == t.mainGoroutineID {
		LogInfo("runOnMain", "already on main goroutine, executing directly")
		fn()
		return true
	}
	op := ipcAction{fn: fn, done: make(chan struct{})}
	LogInfo("runOnMain", "queued op", "pending", len(t.ipcCh), "cap", cap(t.ipcCh))
	select {
	case t.ipcCh <- op:
		LogInfo("runOnMain", "op sent, waiting for execution")
		select {
		case <-op.done:
			LogInfo("runOnMain", "op executed")
			return true
		case <-t.quitCh:
			LogInfo("runOnMain", "quit while waiting")
			return false
		}
	case <-t.quitCh:
		LogInfo("runOnMain", "quit while sending")
		return false
	}
}

// forwardSendToRemote routes a send request to a remote peer by finding the
// RemotePane with matching Host() and calling SendText. The RemotePane's
// SendText sends the command over the yamux control stream to the daemon.
//
// The reply is the peer's (ini-58m8): OK only once the other machine accepted
// the send, otherwise "not delivered" with the peer's error or the transport
// failure. It used to answer OK before anyone had replied.
func (t *TUI) forwardSendToRemote(conn net.Conn, req IPCRequest) {
	rp, errResp := t.findRemoteTarget(req)
	if rp == nil {
		writeIPCResponse(conn, errResp)
		return
	}
	t.sendViaRemote(conn, rp, req)
}

// sendViaRemote sends to an agent another initech owns and answers with that
// owner's verdict (ini-69fy). A peer that answers OK with no verdict answers
// before its verdict, as initech did before ini-69fy: could not confirm,
// never delivered. Its deferral still reads as one (ini-ei8l).
func (t *TUI) sendViaRemote(conn net.Conn, rp *RemotePane, req IPCRequest) {
	name := rp.Host() + ":" + rp.Name()
	resp, err := rp.Request(ControlCmd{Action: "send", Text: req.Text, Enter: req.Enter})
	if reason := peerFailure(resp, err); reason != "" {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("not delivered to %s: %s", name, reason), Delivery: resp.Delivery, Data: resp.Error})
		return
	}
	switch {
	case resp.Delivery != "":
		writeIPCResponse(conn, IPCResponse{OK: true, Delivery: resp.Delivery, Data: resp.Data})
	case strings.Contains(resp.Data, "deferred"):
		writeIPCResponse(conn, IPCResponse{OK: true, Delivery: DeliveryDeferred, Data: resp.Data})
	default:
		writeIPCResponse(conn, IPCResponse{OK: true, Delivery: DeliveryUnconfirmed, Data: fmt.Sprintf(olderPeerVerdictFormat, rp.Host())})
	}
}

// forwardInterruptToRemote sends interrupt host:agent to the peer as an
// interrupt control command (ini-58m8), never as a send: the peer writes
// Escape, or Ctrl+C for "hard", to its own agent. A peer that does not know
// the action answers "unknown action", which is returned as the error.
func (t *TUI) forwardInterruptToRemote(conn net.Conn, req IPCRequest) {
	rp, errResp := t.findRemoteTarget(req)
	if rp == nil {
		writeIPCResponse(conn, errResp)
		return
	}
	resp, err := rp.Request(ControlCmd{Action: "interrupt", Text: req.Text})
	if reason := peerFailure(resp, err); reason != "" {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("not interrupted (%s:%s): %s", req.Host, req.Target, reason)})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true})
}

// findRemoteTarget resolves host:agent to that peer's RemotePane, matching
// host AND name. A missing one yields the error reply naming both.
func (t *TUI) findRemoteTarget(req IPCRequest) (*RemotePane, IPCResponse) {
	var target *RemotePane
	t.runOnMain(func() {
		for _, p := range t.panes {
			if rp, ok := p.(*RemotePane); ok && rp.Host() == req.Host && rp.Name() == req.Target {
				target = rp
				return
			}
		}
	})
	if target == nil {
		return nil, IPCResponse{Error: fmt.Sprintf("agent %q not found on peer %q. Run 'initech peers' to see available targets.", req.Target, req.Host)}
	}
	return target, IPCResponse{}
}

// peerFailure is the reason a forwarded request failed, or "" when the peer
// accepted it.
func peerFailure(resp ControlResp, err error) string {
	switch {
	case err != nil:
		return err.Error()
	case resp.Error != "":
		return resp.Error
	case !resp.OK:
		return "the peer did not accept the request"
	}
	return ""
}

// PeerInfo describes a peer and its agents for the peers_query response.
type PeerInfo struct {
	Name   string   `json:"name"`
	Agents []string `json:"agents"`
}

// handleIPCPeers builds a peer table from local + remote panes and responds
// with a JSON list of PeerInfo.
func (t *TUI) handleIPCPeers(conn net.Conn) {
	var peers []PeerInfo
	t.runOnMain(func() {
		// Group panes by host.
		groups := make(map[string][]string)
		localName := "local"
		if t.project != nil && t.project.PeerName != "" {
			localName = t.project.PeerName
		}
		for _, p := range t.panes {
			host := p.Host()
			if host == "" {
				host = localName
			}
			groups[host] = append(groups[host], p.Name())
		}
		// Local peer first, then remotes in alphabetical order.
		if agents, ok := groups[localName]; ok {
			peers = append(peers, PeerInfo{Name: localName, Agents: agents})
			delete(groups, localName)
		}
		for name, agents := range groups {
			peers = append(peers, PeerInfo{Name: name, Agents: agents})
		}
	})
	data, _ := json.Marshal(peers)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: string(data)})
}

func writeIPCResponse(conn net.Conn, resp IPCResponse) {
	data, _ := json.Marshal(resp)
	conn.Write(data)
	conn.Write([]byte("\n"))
}

// handleIPCAttention marks a pane as waiting on the operator, from the
// consent-gated Notification hook (ini-2x8.4).
//
// The hook is the REDUNDANCY tier: OSC 777 is tier-1 and always on, so this
// path adds a second independent witness for approvals rather than being the
// only signal. It still reports at chime tier, because the payload comes from
// Claude Code declaring its own state -- the same class of evidence as OSC 777,
// not an inference.
//
// Only permission_prompt payloads ever reach here; the CLI filters idle_prompt
// out before sending, since that event fires for every idle agent and would put
// the resting fleet on the list.
func (t *TUI) handleIPCAttention(conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	var pv PaneView
	if !t.runOnMain(func() { pv = t.findPaneByName(req.Target) }) {
		writeIPCResponse(conn, IPCResponse{Error: "TUI shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	lp, ok := pv.(*Pane)
	if !ok {
		// A remote pane's waiting state belongs to the window that owns it.
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q is not local to this session", req.Target)})
		return
	}
	t.runOnMain(func() { lp.SetWaitingInputTier(req.Text, WaitingTierChime) })
	writeIPCResponse(conn, IPCResponse{OK: true})
}

// pendingSubmit is a submit the never-submit belt withheld, held until the
// composer repaints (ini-vpwg).
//
// THE WITHHELD THING IS THE SUBMIT, SO THE RETRY IS THE SUBMIT. The body is
// already sitting in the composer; re-pasting it would double-deliver, which
// is why the modal guard's enqueue-the-whole-message shape is wrong here and
// was correctly left out rather than forgotten.
type pendingSubmit struct {
	bodyLines      []string     // Our body's distinct non-blank lines, the evidence a composer must show to prove it holds OURS.
	pasteCount     int          // The +M a COLLAPSED paste of our body shows: its newline count (ini-vpwg, re-measured ini-k3g2).
	withheldAt     time.Time    // When we withheld, so the operator is told how long a message waited.
	preview        string       // For operator-facing records.
	mode           string       // Injection mode, for the log.
	tailAtWithhold string       // The composer tail when we withheld: "nothing has changed yet".
	seenInBox      bool         // Our body was already seen in the box before the watch began (ini-pkin).
	census         submitCensus // What this send's samples saw, before the Enter and after, for the UNKNOWN line (ini-s1kd).
	deadline       time.Time    // Bounded: we do not wait forever.
}

// withheldSubmitWindow bounds how long a withheld submit waits for the
// composer to repaint. Generous, because the state it is waiting out is a BUSY
// agent and the alternative to waiting is surfacing a message the operator
// then has to re-send by hand.
// Raised from 60s (ini-vpwg repair): the state this waits out is a genuinely
// BUSY agent, measured -- a live Claude writing a 600-word essay does not
// repaint its composer for the belt's whole window -- and a turn that runs
// tools routinely outlasts a minute. The surface message states how long the
// message actually waited, so a generous bound does not become a silent one.
const withheldSubmitWindow = 5 * time.Minute

// submitMarker returns a distinctive slice of the text to look for in the
// composer later. The LAST line is used because the composer tail is one row,
// and a bounded slice because a long paste wraps and only its end stays on the
// tail row.
// newPendingSubmit derives everything a withheld submit needs from the body,
// in ONE place.
//
// A constructor rather than a struct literal at each arming site, because this
// fleet has now paid three times for the same mechanism: a replacement object
// beside a hand-written list of fields to carry over is a silent-omission
// machine, and the omission type-checks. A future arming path gets the
// evidence and the bound whether or not its author remembered them.
func newPendingSubmit(text, mode, tailAtWithhold string) *pendingSubmit {
	preview := text
	if len(preview) > 60 {
		preview = preview[:57] + "..."
	}
	now := time.Now()
	return &pendingSubmit{
		bodyLines:      bodyEvidenceLines(text),
		pasteCount:     collapsedPasteCount(text),
		withheldAt:     now,
		preview:        preview,
		mode:           mode,
		tailAtWithhold: tailAtWithhold,
		deadline:       now.Add(withheldSubmitWindow),
	}
}

// bodyEvidenceLines is the body reduced to what a composer could show us back:
// distinct, non-blank, trimmed lines.
//
// Blank lines are dropped because they are satisfied by any composer at all,
// and evidence that anything satisfies is not evidence.
func bodyEvidenceLines(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

// collapsedPaste matches Claude's collapsed-paste marker and captures the line
// count it exposes: "[Pasted text #1 +39 lines]".
var collapsedPaste = regexp.MustCompile(`\[Pasted text #\d+ \+(\d+) lines?\]`)

// collapsedPasteCount is the +M Claude prints for a collapsed paste of text:
// the number of newline characters in it. MEASURED, not guessed: on Claude
// 2.1.302 one 40-line body read "+39 lines" with no trailing newline, "+40"
// with one and "+41" with two; the first measurement (Claude 2.1.233, a
// 60-line body with no trailing newline, "+59") fits the same rule. The rule
// it replaces, "lines minus one" with trailing newlines trimmed, called every
// placeholder for a message ending in a newline someone else's.
//
// The comparison against it is EXACT. If a future Claude prints a different
// count, this comment is the instruction: RE-MEASURE, do not add a tolerance.
// A tolerance starts accepting other people's pastes, which is the forged
// submit this whole path exists to avoid.
func collapsedPasteCount(text string) int {
	return strings.Count(text, "\n")
}

// collapsedPasteMeasuredVersion must change alongside the measurement above.
// Surfaced failures name this version so a stale proof is diagnosable.
const collapsedPasteMeasuredVersion = "2.1.302"

// minProvenRunes is how much of OUR OWN text must be visible in the composer
// before we will call it proof.
//
// THIS IS A FLOOR ON EVIDENCE, NOT ON MESSAGE LENGTH, and the difference is
// the whole finding (eng2, against 1529dca). A body ending "...\nthanks"
// yielded a six-rune marker, and the branch that handled it required the
// composer to hold that and nothing else. Strictness is not disambiguation:
// our own paste of "thanks" and the OPERATOR typing "thanks" produce a
// BYTE-IDENTICAL row, so no inspection of it can tell them apart. The branch
// read as a proof and was an assumption -- and it assumed in the state where
// the belt suspected a DIALOG had swallowed our paste, which makes a wrong
// answer the belt answering a picker on the operator's behalf. The words that
// collide are "ok", "done", "thanks": the ones both sides use.
//
// So we no longer ask "is our last line on the last row". We ask how much of
// our body is visible ANYWHERE in the composer, and require enough of it that
// a coincidence is not a plausible explanation. A message whose entire visible
// content is below this floor -- a body that is just "ok" -- cannot be proven
// by anything on screen, and SURFACES rather than being guessed at.
//
// TWELVE STAYS, and its collision classes are recorded here rather than
// designed around (eng2 raised the number and then argued against moving it;
// super agreed). Twelve runes of coincidence is implausible for prose. Two
// ways it can still be reached, both measured:
//
//   - THE SAME COMMAND. Both sides of this fleet type the same things, so a
//     body line like "git pull --rebase" can be satisfied by an operator
//     typing that command while our paste was withheld. This was reachable
//     before the space-stripping and is the dominant case.
//   - ACROSS WORD BOUNDARIES. provenRunes compares SPACE-STRIPPED forms, so
//     the colliding text need not contain our line as written -- only its
//     non-blank runes, contiguously and in order. Measured: our line "git
//     pull --rebase" against a composer reading "we should legit pull
//     --rebase-this later" proves 15 runes, because "legit pull" supplies
//     "gitpull". Recorded because a reader will assume word boundaries
//     constrain this, BECAUSE THEY NORMALLY DO. Order is still required (a
//     scrambled composer proves 0) and unrelated text proves 0, so the
//     widening is contiguity, not arbitrary matching.
//
// The widening is accepted, not overlooked: it is narrower than the
// same-command case above, and the alternative to stripping is the delivery
// bug it fixed, which fires on ordinary traffic rather than on a coincidence.
// If this ever bites, the answer is MORE EVIDENCE, not a bigger number --
// raising the floor trades a rare forge for routine surfacing.
const minProvenRunes = 12

// composerBlock returns the composer's visible text: the prompt row's content
// followed by every row beneath it.
//
// Deliberately layout-agnostic. Claude puts the first line of a multi-line
// body on the prompt row and the rest below it, so "the last row" is not where
// our last line lives -- but the proof below never needs to know that, because
// it asks whether our text is present in the block at all, not where. A proof
// that depends on an unmeasured layout detail is a proof that breaks silently
// when the layout changes.
//
// A second reason, measured by eng2: the tail scan stops at the lowest row
// containing ">" ANYWHERE, so a continuation row that happens to contain a ">"
// becomes the "prompt" row and this block starts MID-BODY. Asking presence
// rather than position is what makes that harmless -- the proof survives a
// composer layout nobody has fully characterised, which is the property to
// preserve if this is ever rewritten.
func composerBlock(p *Pane) ([]string, bool) {
	tail, row, found := composerTailAt(p)
	if !found {
		return nil, false
	}
	rows := []string{tail}
	cols := p.emu.Width()
	for r := row + 1; r < p.emu.Height(); r++ {
		rows = append(rows, strings.TrimSpace(p.emu.RowText(r, cols)))
	}
	return rows, true
}

// provenRunes counts how many runes of OUR body are visible in the composer.
//
// WHITESPACE IS STRIPPED FROM BOTH SIDES BEFORE COMPARING, and that is not
// tidiness -- it is the fix for a delivery bug eng2 found (review of bc92fdb).
// Wrapping splits a line without inserting anything, so joining the rows with
// no separator rebuilds it; but composerBlock must TrimSpace each row, because
// RowText pads to the pane width, and when the split lands exactly ON A SPACE
// the trim then deletes a character the wrap left in place. Concatenation no
// longer rebuilds the line, the line proves ZERO -- provenRunes is
// all-or-nothing per line -- and a single long line surfaces as undelivered
// while sitting plainly in the operator's composer, where a re-send gives the
// agent the message twice.
//
// Comparing space-stripped forms cannot be broken by WHERE the split lands,
// which is a property of the text and the pane width that nobody measured and
// nothing guarantees. It also makes the trim above harmless rather than
// load-bearing. The cost is that a body line's weight is now its non-blank
// rune count, which is the more honest measure anyway: spaces are the cheapest
// thing for a coincidence to supply.
func provenRunes(rows, bodyLines []string) int {
	joined := stripSpace(strings.Join(rows, ""))
	seen := make(map[string]bool, len(bodyLines))
	n := 0
	for _, line := range bodyLines {
		line = stripSpace(line)
		if line == "" || seen[line] || !strings.Contains(joined, line) {
			continue
		}
		seen[line] = true
		n += len([]rune(line))
	}
	return n
}

// stripSpace removes every whitespace rune, so a comparison cannot depend on
// where a terminal chose to wrap.
func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// composerProvesOurPaste reports whether the repainted composer is provably
// holding OUR body (ini-vpwg repair).
//
// THE TRANSITION IS REQUIRED, not just the state: a tail equal to the one we
// withheld on proves nothing, and a placeholder that was already sitting there
// from an earlier paste is not evidence of ours.
//
// TWO PROOFS, because Claude renders a paste two different ways:
//   - SMALL paste: the composer echoes our text, so enough of our own body
//     is visible somewhere in it to rule out coincidence.
//   - LARGE paste: Claude COLLAPSES it to "[Pasted text #N +M lines]" and our
//     text is nowhere in the block. Requiring our text there rejected our own
//     paste as somebody else's and surfaced every long message -- measured
//     against the landed code before this repair.
func composerProvesOurPaste(p *Pane, tail string, ps *pendingSubmit) bool {
	if tail == ps.tailAtWithhold {
		return false // Nothing has repainted; this is the keep-waiting case.
	}
	return composerHoldsOurBody(p, tail, ps)
}

// composerHoldsOurBody is composerProvesOurPaste's content proof without the
// transition requirement: does the composer, right now, show OUR body? The
// belt needs the transition; the submit confirmation (ini-nma5) needs only
// the state, so the proof is shared and the belt's behaviour is unchanged.
func composerHoldsOurBody(p *Pane, tail string, ps *pendingSubmit) bool {
	if rows, ok := composerBlock(p); ok && provenRunes(rows, ps.bodyLines) >= minProvenRunes {
		return true
	}
	m := collapsedPaste.FindStringSubmatch(tail)
	if m == nil {
		return false
	}
	shown, err := strconv.Atoi(m[1])
	if err != nil {
		// Corroboration could not run; say so rather than let a reader believe
		// a size check happened. The collapsed shape is still our evidence.
		LogInfo("inject", "collapsed paste line count unparseable; size corroboration SKIPPED",
			"shown", m[1])
		return true
	}
	// "A paste landed" becomes "a paste of OUR SIZE landed" for one comparison.
	//
	// EXACT, not within-a-line-or-two. The first version allowed a slack of
	// one and a mutant that zeroed the then line offset survived every test in
	// the suite: the slack was wide enough to swallow the very constant it was
	// corroborating, which makes the measurement unfalsifiable and admits a
	// neighbouring paste that is not ours. If Claude ever counts differently,
	// this stops proving and we SURFACE -- the safe direction -- and the fix
	// is to re-measure collapsedPasteCount, never to widen this.
	if ps.pasteCount != shown {
		return false // Somebody else's paste is in the composer.
	}
	return true
}

// maybeRetryWithheldSubmit is called on every chunk of pane output. It sends a
// withheld submit ONLY when the composer provably still holds our text, and
// otherwise surfaces rather than guessing.
func (p *Pane) maybeRetryWithheldSubmit() {
	p.mu.Lock()
	ps := p.pendingSubmit
	p.mu.Unlock()
	if ps == nil {
		return
	}

	// THE LOCK SPANS THE CHECK AND THE WRITE (ini-vpwg repair, finding 3).
	// This runs on the readLoop goroutine; sendSubmitKey used to be called
	// with no send lock at all, so a concurrent sender could be mid-bracketed
	// paste on this pane when the submit landed -- inside somebody else's
	// half-written body. Its sibling hook one line up (maybeDrainModalQueue)
	// re-delivers through SendText, which DOES take sendMu; this one did not.
	//
	// TryLock, NOT Lock, and this fork was decided rather than defaulted.
	//
	//   Lock     -> correct every time, but blocks the readLoop behind a
	//               sender that can hold sendMu through a whole bracketed
	//               paste. readLoop is what feeds the emulator, so that is a
	//               RENDER STALL on this pane: we would trade a forged submit
	//               for a frozen screen.
	//   TryLock  -> never stalls rendering, but the retry cannot run at the
	//               moment output arrives while a sender holds the lock. A
	//               SILENTLY SKIPPED RETRY WOULD BE THIS BEAD'S OWN BUG AGAIN,
	//               so it is only safe with a re-arm path.
	//
	// TryLock is chosen because the re-arm path exists and is cheap: the
	// pending submit is NOT cleared here, so the very next chunk of output
	// retries it, and output is exactly what a repainting composer produces.
	// The case with no further output at all -- the one a re-arm cannot cover,
	// and the one where a declined round would otherwise compound with a
	// deadline that never fires -- is covered by sweepWithheldSubmits, which
	// runs off the maintenance tick and needs the pane to say nothing.
	if !p.sendMu.TryLock() {
		return
	}
	defer p.sendMu.Unlock()

	tail, found := composerTail(p)
	switch {
	// OUR TEXT VISIBLE needs no transition (ini-cg8y): the transition guards the
	// collapsed-placeholder proof, where an earlier paste's placeholder could
	// look like ours; our own runes cannot. Requiring it here is why the retry
	// delivered 0 times in a month while bodies sat in the composer.
	case (found && composerProvesOurPaste(p, tail, ps)) || bodyTextVisible(p, ps):
		// PROVABLE: our text is in the composer. Send the submit, nothing else.
		// Claim it first: if the sweep already reported this message as
		// undelivered, sending it now would make that report a lie.
		p.deliverWithheldSubmit(ps, "composer repainted holding our text")
	case found && tail == ps.tailAtWithhold:
		// Nothing has changed yet. Keep waiting, but not forever.
		if time.Now().After(ps.deadline) {
			p.surfaceUndeliveredSubmit(ps, "the composer never repainted")
		}
	default:
		// The composer changed to something that is NOT ours, or vanished. We
		// genuinely do not know whether our text is still there, and a submit
		// sent into an unknown composer is a FORGED submit -- the exact thing
		// the belt exists to prevent. Do not guess; surface.
		p.surfaceUndeliveredSubmit(ps, "the composer no longer holds our text")
	}
}

// deliverWithheldSubmit sends a withheld submit whose body is provably in the
// composer, and records it as DELIVERED -- the one fate line, in the one
// event format the toast, event log and inbox read. Caller holds sendMu.
// Claimed first: if another resolver already reported it, sending now would
// make that report a lie.
func (p *Pane) deliverWithheldSubmit(ps *pendingSubmit, how string) bool {
	// NEVER INTO A DIALOG (ini-cg8y, from a security review of this change).
	// The retry runs on pane output with no modal guard of its own, and it
	// now delivers on our text being visible rather than on a row change.
	// A dialog that happens to show text matching our body -- a permission
	// prompt quoting a command, say -- must never be answered by this Enter.
	// So layer 1 is consulted here, at the one place a withheld submit is
	// sent: with a dialog up we keep waiting, and the deadline sweep still
	// bounds the wait.
	if paneHasModal(p) {
		return false
	}
	if !p.takePendingSubmit(ps) {
		return false
	}
	sendSubmitKey(p.emu, p.submitKey)
	LogInfo("inject", "withheld submit DELIVERED: "+how,
		"pane", p.name, "mode", ps.mode, "waited", time.Since(ps.withheldAt).Round(time.Second), "text", ps.preview)
	p.reportEvent(AgentEvent{
		Type: EventMessageSent,
		Pane: p.name,
		Detail: deliveredLateSubmitPrefix +
			time.Since(ps.withheldAt).Round(time.Second).String() + " held): " + ps.preview,
	})
	return true
}

// settlePendingSubmit gives a withheld submit its fate before the next send
// reaches the composer (ini-cg8y, AC 2). Before this the next send simply
// carried on: a second withhold overwrote the first with no fate line, and
// the new paste changed the composer under the old retry, which then
// reported a FALSE "no longer holds our text". The next send's stash would
// also tuck a stranded body away as a draft that is never sent.
// So: if OUR old body is provably still in the composer it is delivered
// now; otherwise it is SUPERSEDED, reported not delivered. Exactly one
// terminal line either way. Caller holds sendMu.
func (p *Pane) settlePendingSubmit() {
	p.mu.Lock()
	ps := p.pendingSubmit
	p.mu.Unlock()
	if ps == nil {
		return
	}
	tail, found := composerTail(p)
	if (found && composerProvesOurPaste(p, tail, ps)) || bodyTextVisible(p, ps) {
		if p.deliverWithheldSubmit(ps, "settled by the next send, our text still in the composer") {
			// Let Claude take it before the next send's stash and paste.
			time.Sleep(bracketedPasteSubmitDelay)
		}
		return
	}
	if !p.takePendingSubmit(ps) {
		return
	}
	waited := time.Since(ps.withheldAt).Round(time.Second)
	LogInfo("inject", "withheld submit SUPERSEDED: a later send arrived before it could be delivered",
		"pane", p.name, "mode", ps.mode, "waited", waited, "text", ps.preview,
		"tail_now", logSnippet(tail), "composer", composerSnapshot(p))
	p.emitUndeliveredSubmit(ps, "superseded by a later send before its submit went out", waited)
}

func (p *Pane) clearPendingSubmit() {
	p.mu.Lock()
	p.pendingSubmit = nil
	p.mu.Unlock()
}

// takePendingSubmit claims a withheld submit for exactly one resolver.
//
// There are now two paths that can resolve a message -- the output hook and
// the sweep -- and a plain "read it, then clear it" lets both act on the same
// pendingSubmit: the operator gets told a message was NOT delivered by one
// path while the other is delivering it. Compare-and-clear under the pane lock
// makes resolution a single atomic event; the loser does nothing at all.
func (p *Pane) takePendingSubmit(ps *pendingSubmit) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pendingSubmit != ps {
		return false
	}
	p.pendingSubmit = nil
	return true
}

// sweepWithheldSubmits enforces the withheld-submit deadline on panes that are
// producing no output (ini-vpwg, finding 4).
//
// It deliberately does NOT try to deliver: delivery needs proof from a
// repainted composer, and a pane that has said nothing has offered none. The
// only honest act at the deadline is to tell the operator.
func (t *TUI) sweepWithheldSubmits(now time.Time) {
	sweepWithheldSubmitsIn(t.panes, now)
}

// sweepWithheldSubmitsIn is sweepWithheldSubmits over a given pane list, so
// a daemon's panes get the same bound (ini-dbqy).
func sweepWithheldSubmitsIn(panes []PaneView, now time.Time) {
	for _, pv := range panes {
		p, ok := pv.(*Pane)
		if !ok {
			continue
		}
		p.mu.Lock()
		ps := p.pendingSubmit
		p.mu.Unlock()
		if ps == nil || now.Before(ps.deadline) {
			continue
		}
		p.surfaceUndeliveredSubmit(ps, "the pane produced no further output")
	}
}

// surfaceUndeliveredSubmit reports a message we could not deliver and could not
// prove the fate of. Loud on purpose: at this point the honest statement is "we
// do not know", and the operator is the only one who can resolve it.
func (p *Pane) surfaceUndeliveredSubmit(ps *pendingSubmit, why string) {
	if !p.takePendingSubmit(ps) {
		return // Another resolver got there first; one message, one report.
	}
	waited := time.Since(ps.withheldAt).Round(time.Second)
	// The screen fields are diagnostics: a pane with no screen still gets its
	// report (ini-cg8y).
	tail, found, composer, inBlock := "", false, "(no screen)", false
	if p.emu != nil {
		tail, found = composerTail(p)
		composer, inBlock = composerSnapshot(p), found && submitStillHeld(p, tail, ps)
	}
	LogInfo("inject", "withheld submit NOT DELIVERED", "pane", p.name, "mode", ps.mode,
		"reason", why, "waited", waited, "text", ps.preview,
		"tail_at_withhold", logSnippet(ps.tailAtWithhold), "tail_now", logSnippet(tail),
		"composer", composer, "body_in_block", inBlock,
		"loop_missed_last_min", loopMissedLastMinute.Load())
	p.emitUndeliveredSubmit(ps, why, waited)
}

// emitUndeliveredSubmit is the operator-facing half of an undelivered report:
// the "NOT delivered (...)" event the toast, the event log and the inbox all
// read. Shared by the belt and the submit confirmation (ini-nma5) so both
// speak one format.
func (p *Pane) emitUndeliveredSubmit(ps *pendingSubmit, why string, waited time.Duration) {
	// The WAIT is in the message: a bound that is generous still leaves the
	// operator needing to know a message sat for 90s rather than arriving
	// late for no stated reason (super, 2026-08-22).
	p.emitSubmitReport(undeliveredSubmitPrefix,
		"after "+waited.String()+" ("+why+"), re-send it: "+ps.preview)
}

// emitUnconfirmedSubmit reports a submit whose fate the send path could not
// see either way (ini-k3g2): the box never showed our text, or showed a
// placeholder that is not ours. Not "NOT delivered" -- it may well have
// arrived -- and never "delivered": the operator is the one who can look.
func (p *Pane) emitUnconfirmedSubmit(ps *pendingSubmit, why string, waited time.Duration) {
	p.emitSubmitReport(unconfirmedSubmitPrefix,
		"after "+waited.String()+" ("+why+"), re-send it if the agent did not get it: "+ps.preview)
}

// emitSubmitReport sends one submit report event: the prefix, the measured
// Claude version pair, then the detail.
func (p *Pane) emitSubmitReport(prefix, detail string) {
	event := AgentEvent{
		Type:   EventAgentStalled,
		Pane:   p.name,
		Detail: detail,
		Time:   time.Now(),
	}
	// This can run on readLoop while sendMu is held, or on maintenance. The
	// version lookup must not hold either caller up. Capture the event/channel
	// now, so asynchronous reporting never reads mutable pane state.
	probe := p.claudeVersion
	if probe == nil {
		probe = sessionClaudeVersion
	}
	events, sink := p.eventCh, p.reportSink.Load()
	probe.report(func(versions string) {
		event.Detail = prefix + versions + ") " + event.Detail
		deliverReport(events, sink, event)
	})
}

// deferredMsg renders a deferral a sender can act on: how long the latch has
// been up and how much mail is behind it.
func deferredMsg(target string, age time.Duration, queued int) string {
	if age <= 0 {
		return fmt.Sprintf("deferred to %s — modal open, %d queued", target, queued)
	}
	return fmt.Sprintf("deferred to %s — modal open %s, %d queued",
		target, age.Round(time.Minute), queued)
}

// peekRead is what one peek read found.
type peekRead struct {
	text  string
	found int  // lines returned
	alt   bool // the pane is on its alternate screen: screen only
	rows  int  // the pane's height
	kept  int  // the most lines of history the pane keeps
}

// readHistory reads a pane as a tall terminal would show it (ini-ctrw): the
// main screen's scrollback, oldest first, then the screen, last `lines` lines
// after trailing blank rows are trimmed. Scrollback is read only when the
// screen alone may be short of `lines`, and no more of it than could be
// needed. A line leaves the screen's top as it enters scrollback, so the two
// never overlap and nothing repeats at the seam. On the alternate screen (an
// editor, a pager) the scrollback belongs to the screen behind it, so the
// screen alone is read. lines <= 0 takes no scrollback: the screen alone, as
// before.
//
// The caller holds the emulator's lock: peekReadBlocking waits for it on an
// IPC goroutine, peekContent tries for it from the main loop.
func readHistory(e *vt.Emulator, lines int) peekRead {
	screen := readScreenRows(e)
	r := peekRead{alt: e.IsAltScreen(), rows: e.Height()}
	all := screen
	if sb := e.Scrollback(); sb != nil && !r.alt {
		r.kept = sb.MaxLines()
		from := max(sb.Len()-lines, 0)
		hist := make([]string, 0, sb.Len()-from+len(screen))
		for i := from; i < sb.Len(); i++ {
			hist = append(hist, sb.Line(i).String())
		}
		all = append(hist, screen...)
	}
	r.text = peekLastLines(all, lines)
	r.found = strings.Count(r.text, "\n")
	return r
}

// peekReadBlocking is the peek handler's read: it waits for the emulator's
// lock, holding it for the scrollback and the screen together so a line
// cannot move between them mid-read. IPC goroutine and daemon only, never
// the main loop (lock-discipline allowlist).
//
// WHY TWO FORMS (ini-oxnl): psjt's bounded try protects the MAIN LOOP from
// parking behind a wedged pane. Applied to every caller, it also made peek
// on Linux answer "pane unreadable" for the whole of an agent's sustained
// output burst -- the scheduler there never yields a gap the 250ms try can
// win, while macOS does (v2.13.0 release run 34702544631). A goroutine that
// exists to answer one request has no display to protect and may wait; the
// lock-discipline inventory keeps the two forms on the right callers.
func peekReadBlocking(p PaneView, lines int) peekRead {
	emu := p.Emulator()
	if emu == nil {
		return peekRead{}
	}
	emu.Lock()
	defer emu.Unlock()
	return readHistory(emu.Emulator, lines)
}
