package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	Action string `json:"action"`          // "send", "peek", "list", "peers_query"
	Target string `json:"target"`          // Role name (for send/peek).
	Host   string `json:"host"`            // Remote peer name (for cross-machine send). Empty = local.
	Text   string `json:"text"`            // Text to inject (for send).
	Lines  int    `json:"lines"`           // Number of lines to return (for peek, 0 = all).
	Enter  bool   `json:"enter"`           // Append Enter after text (for send).
	Prune  bool   `json:"prune,omitempty"` // reload: also remove deconfigured self-started agents.
}

// IPCResponse is the JSON structure returned by the TUI socket.
type IPCResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  string `json:"data,omitempty"` // Pane content for peek, pane list for list.
}

// SocketPath returns the IPC endpoint path for a project. On Unix this is a
// domain socket inside .initech/; on Windows it is a named pipe.
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
	var panes []PaneInfo
	ok := t.runOnMain(func() {
		panes = make([]PaneInfo, len(t.panes))
		for i, p := range t.panes {
			panes[i] = PaneInfo{
				Name:     p.Name(),
				Host:     p.Host(),
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  !t.layoutState.Hidden[agentKey(p)],
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
	return panes, ok
}

func (t *TUI) HandleSend(conn net.Conn, req IPCRequest) {
	t.handleIPCSend(conn, req)
}

func (t *TUI) HandleExtended(conn net.Conn, req IPCRequest, rawJSON []byte) bool {
	switch req.Action {
	case "stop":
		t.handleIPCStop(conn, req)
	case "start":
		t.handleIPCStart(conn, req)
	case "restart":
		t.handleIPCRestart(conn, req)
	case "reload":
		t.handleIPCReload(conn, req)
	case "bead":
		t.handleIPCBead(conn, req)
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
	if !t.runOnMain(func() {
		pv = t.findPaneByName(req.Target)
		if pv == nil {
			return
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
	if queued {
		// Trigger resume-on-message: respawn the agent and drain the queue.
		// This blocks until the agent initializes and messages are delivered.
		if err := t.resumePane(concretePane, "incoming message"); err != nil {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("resume failed: %v", err)})
			return
		}
		writeIPCResponse(conn, IPCResponse{OK: true, Data: `"resumed and delivered"`})
		return
	}

	// Detect a Claude modal on the local target before delivery so the sender is
	// told the send was deferred rather than receiving a misleading "delivered"
	// (ini-7txh). Best-effort for the response only — sendPaneTextLocked is
	// authoritative for the actual defer/queue and emits its own operator event.
	modalDeferred := false
	// Age and depth travel with the deferral (ini-gbqc): "deferred" alone
	// cannot tell a sender a dialog opened a second ago from one that has held
	// their mail for hours, and the second is the case worth acting on. The
	// live report was hours of silent deferral, diagnosed only by a human
	// peeking at the pane.
	var latchAge time.Duration
	var queuedDepth int
	if lp, ok := pv.(*Pane); ok {
		t.runOnMain(func() {
			modalDeferred = paneHasModal(lp)
			latchAge, _ = lp.latchAge(time.Now())
			queuedDepth = lp.QueuedMessageCount()
		})
	}

	// Normal path: deliver via PaneView.SendText (works for local and remote).
	pv.SendText(req.Text, req.Enter)

	if modalDeferred {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: deferredMsg(req.Target, latchAge, queuedDepth)})
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
	writeIPCResponse(conn, IPCResponse{OK: true})
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
func sendPaneTextLocked(pane *Pane, text string, enter bool) {
	if pane.ptmx == nil {
		return
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
		dropped := pane.EnqueueMessage(text, enter)
		preview := text
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		// INFO, not Debug (ini-9gvn): the live incident left ZERO trace in a
		// 7.8MB fleet log because this was the only record and it was below the
		// default level. A message the fleet is holding is not debug detail.
		LogInfo("inject", "deferred: modal open", "pane", pane.Name(), "enter", enter,
			"queued", pane.QueuedMessageCount(), "queue_dropped", dropped)
		EmitEvent(pane.eventCh, AgentEvent{
			Type:   EventMessageSent,
			Pane:   pane.Name(),
			Detail: "deferred (modal open): " + preview,
		})
		return
	}

	useCodexBracketedPaste := pane.noBracketedPaste && pane.AgentType() == config.AgentTypeCodex
	codexQueueSubmit := pane.AgentType() == config.AgentTypeCodex && pane.Activity() == StateRunning
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

	if !enter {
		return
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
	if beltApplies && hasComposer && !composerAcceptedBody(pane, tailBefore) {
		withholdSubmit(pane, text, mode)
		return
	}

	if useCodexBracketedPaste {
		time.Sleep(codexBracketedSubmitDelay)
		method := sendCodexSubmit(pane, codexQueueSubmit)
		LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", method, "delay_ms", codexBracketedSubmitDelay.Milliseconds())
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
		return
	}

	if pane.noBracketedPaste {
		// Let Codex's non-bracketed paste-burst detection expire before sending
		// Enter. The 8ms parser window observed in source was not enough in the
		// full PTY/TUI pipeline; use a larger margin here.
		time.Sleep(rawSubmitDelay)
		LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", "pty-enter", "delay_ms", rawSubmitDelay.Milliseconds())
		if config.IsCodexLikeAgentType(pane.AgentType()) && pane.submitKey != "ctrl+enter" {
			_, _ = pane.ptmx.Write([]byte("\r"))
			return
		}
		sendSubmitKey(pane.emu, pane.submitKey)
		return
	}

	// Bracketed paste mode: wait for Claude Code's paste completion
	// (100ms PASTE_COMPLETION_TIMEOUT_MS) plus async rendering time.
	time.Sleep(bracketedPasteSubmitDelay)
	LogDebug("inject", "submit", "pane", pane.Name(), "mode", mode, "method", "emulator", "delay_ms", bracketedPasteSubmitDelay.Milliseconds())
	sendSubmitKey(pane.emu, pane.submitKey)

	// Single retry for large pastes where Enter was swallowed. Skip when a
	// stash was active because Claude Code auto-restores stashed text into the
	// prompt after submit, making promptHasContent a false positive that would
	// submit the operator's unfinished text (ini-vxw).
	if pane.IsAlive() && !stashed {
		time.Sleep(bracketedPasteSubmitDelay)
		if promptHasContent(pane) {
			LogDebug("inject", "submit retry", "pane", pane.Name(), "mode", mode, "method", "emulator")
			sendSubmitKey(pane.emu, pane.submitKey)
		}
	}
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
	codexQueueSubmit := pane.AgentType() == config.AgentTypeCodex && pane.Activity() == StateRunning
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
	cols := p.emu.Width()
	rows := p.emu.Height()
	for row := rows - 1; row >= 0; row-- {
		// RowText copies the row under a single lock, so a torn read here
		// cannot flip the submit decision (ini-wizq).
		text := p.emu.RowText(row, cols)
		for _, prompt := range []string{"\u276f", "\u203a", ">"} {
			if idx := strings.LastIndex(text, prompt); idx >= 0 {
				return strings.TrimSpace(text[idx+len(prompt):]), row, true
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
func composerAcceptedBody(p *Pane, before string) bool {
	deadline := time.Now().Add(composerEchoWindow)
	for {
		// A vanished glyph counts as NOT accepted: the comparison is only
		// meaningful against a composer that is still there to have accepted it.
		if after, found := composerTail(p); found && after != before {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(composerEchoPoll)
	}
}

// withholdSubmit records a submit the belt refused to send, and ARMS a retry
// for it (ini-vpwg). Before this, withholding was the end of the story: the
// body sat in the composer, the submit never went, and nothing re-delivered
// it -- a message the fleet was holding with no holder.
func withholdSubmit(pane *Pane, text, mode string) {
	tail, _ := composerTail(pane)
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
	LogInfo("inject", "submit WITHHELD: body never reached a composer", "pane", pane.Name(), "mode", mode)
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
		Name     string `json:"name"`
		Activity string `json:"activity"`
		Bead     string `json:"bead,omitempty"`
		Alive    bool   `json:"alive"`
		Visible  bool   `json:"visible"`
		Content  string `json:"content"`
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
	emu := p.Emulator()
	cols := emu.Width()
	emuRows := emu.Height()

	allLines := make([]string, emuRows)
	for row := 0; row < emuRows; row++ {
		// RowText copies the row under a single lock, so this cannot observe
		// a torn cell from a concurrent readLoop write (ini-wizq). peekContent
		// is reached from IPC/daemon handler goroutines and the main-loop
		// patrol/:peek paths, none of which hold p.renderMu.
		allLines[row] = strings.TrimRight(emu.RowText(row, cols), " ")
	}

	// Strip trailing blank lines.
	contentEnd := emuRows
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
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required (set INITECH_AGENT or use --agent)"})
		return
	}
	// Validate bead text before touching TUI state. Tab is allowed as the
	// id\ttitle delimiter; other control characters are rejected.
	if len(req.Text) > 256 {
		writeIPCResponse(conn, IPCResponse{Error: "bead text too long (max 256 chars)"})
		return
	}
	for _, ch := range req.Text {
		if ch != '\t' && (ch < 0x20 || ch == 0x7F) {
			writeIPCResponse(conn, IPCResponse{Error: "bead text contains control characters"})
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
		// SUSPENDED IS NOT DEAD (ini-g7fl): the old message said "restart it",
		// which fights resume-on-message -- a restart discards the queue and
		// the suspension bookkeeping. Name the state and the designed remedy.
		if pv.IsSuspended() {
			writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf(
				"pane %q is suspended; it resumes on message (initech send %s ...) and the bead can be set then",
				req.Target, req.Target)})
			return
		}
		// A dead pane's process has exited; silently "succeeding" here would
		// mean the bead display update is lost the moment the pane is
		// restarted (NewPane starts with no bead state), indistinguishable
		// from the request never having been sent (ini-cs7).
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q is not alive (agent process has exited); restart it before setting its bead", req.Target)})
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
func (t *TUI) forwardSendToRemote(conn net.Conn, req IPCRequest) {
	var target PaneView
	t.runOnMain(func() {
		for _, p := range t.panes {
			if p.Host() == req.Host && p.Name() == req.Target {
				target = p
				return
			}
		}
	})
	if target == nil {
		writeIPCResponse(conn, IPCResponse{
			Error: fmt.Sprintf("agent %q not found on peer %q. Run 'initech peers' to see available targets.", req.Target, req.Host),
		})
		return
	}
	target.SendText(req.Text, req.Enter)
	writeIPCResponse(conn, IPCResponse{OK: true})
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
	bodyLines      []string  // Our body's distinct non-blank lines, the evidence a composer must show to prove it holds OURS.
	lines          int       // Our body's line count, to corroborate a COLLAPSED paste (ini-vpwg repair).
	withheldAt     time.Time // When we withheld, so the operator is told how long a message waited.
	preview        string    // For operator-facing records.
	mode           string    // Injection mode, for the log.
	tailAtWithhold string    // The composer tail when we withheld: "nothing has changed yet".
	deadline       time.Time // Bounded: we do not wait forever.
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
		lines:          countBodyLines(text),
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

// collapsedPasteLineOffset is MEASURED, not guessed (eng1, live Claude
// 2.1.233, 2026-08-22): a 60-line body renders as "[Pasted text #1 +59
// lines]". The count Claude prints is our line count MINUS ONE.
//
// This is why the tolerance below is one -- it is the convention, not slop. If
// a future Claude prints a different count, this comment is the instruction:
// RE-MEASURE the offset, do not widen the tolerance. Widening starts accepting
// other people's pastes, which is the forged submit this whole path exists to
// avoid.
const collapsedPasteLineOffset = 1

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
	// one and a mutant that zeroed collapsedPasteLineOffset survived every
	// test in the suite: the slack was wide enough to swallow the very
	// constant it was corroborating, which makes the measurement unfalsifiable
	// and admits a neighbouring paste that is not ours. If Claude ever counts
	// differently, this stops proving and we SURFACE -- the safe direction --
	// and the fix is to re-measure the offset, never to widen this.
	if ps.lines-collapsedPasteLineOffset != shown {
		return false // Somebody else's paste is in the composer.
	}
	return true
}

// countBodyLines counts the lines of a body as Claude would see them.
func countBodyLines(text string) int {
	return len(strings.Split(strings.TrimRight(text, "\n"), "\n"))
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
	case found && composerProvesOurPaste(p, tail, ps):
		// PROVABLE: our text is in the composer. Send the submit, nothing else.
		// Claim it first: if the sweep already reported this message as
		// undelivered, sending it now would make that report a lie.
		if !p.takePendingSubmit(ps) {
			return
		}
		sendSubmitKey(p.emu, p.submitKey)
		LogInfo("inject", "withheld submit DELIVERED: composer repainted holding our text",
			"pane", p.name, "mode", ps.mode, "text", ps.preview)
		EmitEvent(p.eventCh, AgentEvent{
			Type: EventMessageSent,
			Pane: p.name,
			Detail: "delivered after the composer repainted (" +
				time.Since(ps.withheldAt).Round(time.Second).String() + " held): " + ps.preview,
		})
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
	for _, pv := range t.panes {
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
	LogInfo("inject", "withheld submit NOT DELIVERED", "pane", p.name, "mode", ps.mode,
		"reason", why, "waited", waited, "text", ps.preview)
	EmitEvent(p.eventCh, AgentEvent{
		Type: EventAgentStalled,
		Pane: p.name,
		// The WAIT is in the message: a bound that is generous still leaves the
		// operator needing to know a message sat for 90s rather than arriving
		// late for no stated reason (super, 2026-08-22).
		Detail: "message NOT delivered after " + waited.String() + " (" + why + "), re-send it: " + ps.preview,
		Time:   time.Now(),
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
