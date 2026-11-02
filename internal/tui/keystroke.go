package tui

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// keystrokeKeys is every key initech keystroke can deliver, by the name the
// command takes, and the exact bytes written to the pane for it: one write,
// nothing before or after it (no bracketed paste, no stash, no Enter).
var keystrokeKeys = map[string]string{
	"enter":  "\r",
	"esc":    "\x1b",
	"ctrl-c": "\x03",
	"y":      "y",
	"n":      "n",
}

// KeystrokeKeyNames is the key names initech keystroke takes, in the order
// its help and refusal line give them.
var KeystrokeKeyNames = []string{"enter", "esc", "ctrl-c", "y", "n"}

// KeystrokeKnown reports whether name is a key initech keystroke delivers.
func KeystrokeKnown(name string) bool {
	_, ok := keystrokeKeys[name]
	return ok
}

// KeystrokeUnknownReason is the refusal for a key name outside the table.
func KeystrokeUnknownReason(name string) string {
	return fmt.Sprintf("unknown key %q; keys are enter, esc, ctrl-c, y, n", name)
}

// keystrokeDNDReason is why an agent's key to a pane in do-not-disturb is not
// delivered: a key is about the screen now, so it is never queued.
func keystrokeDNDReason(name string, age time.Duration) string {
	return fmt.Sprintf("%s is in do-not-disturb (%s); a key is never queued", name, shortAge(age))
}

// keystrokeDialogReason is why an agent's key to a pane with a dialog open is
// not delivered, super --urgent included: answering a dialog (a permission
// prompt above all) is the operator's, never another agent's.
const keystrokeDialogReason = "a dialog is open; only the operator may answer it"

// keystrokeScreenBudget bounds how long a key tries to read the pane's screen
// for the trust prompt: long enough to outlast a render, short enough that a
// wedged emulator costs an operator's key only this.
var keystrokeScreenBudget = 100 * time.Millisecond

// keystrokeTrustReason is why a key is not delivered at Claude Code's folder
// trust prompt, or "" when it goes in. MEASURED on 2.1.316 (Haiku): Enter
// confirms the highlighted row and "No, exit" is highlighted by default, so
// the process ends; Esc ends it whichever row is highlighted; a second Ctrl-C
// inside its window ends it, and two taps can beat the repaint that shows the
// first was armed, so Ctrl-C is refused outright (one press has no use there);
// y and n do nothing there and go in. Enter on Yes is refused too: trusting a
// folder is a decision taken in the pane. Any other sender gets the dialog
// words, the armed footer being one the dialog guard cannot read.
//
// A screen that cannot be read inside the budget, and a pane no longer alive
// (its last screen can still show the prompt), answer "": the existing verdict
// stands. So a key sent at the instant the screen is held is not guarded.
func keystrokeTrustReason(p *Pane, key string, o sendOrigin) string {
	if !p.IsAlive() {
		return ""
	}
	var rows []string
	deadline := time.Now().Add(keystrokeScreenBudget)
	for {
		var ok bool
		if rows, ok = tryScreenRows(p); ok {
			break
		}
		if !time.Now().Before(deadline) {
			p.noteScreenSkipped(time.Now(), "keystroke")
			return ""
		}
	}
	cursor := trustPromptCursor(strings.Join(liveDialogRows(rows), "\n"))
	if cursor == trustNotShown {
		return ""
	}
	if o.From != operatorSender {
		return keystrokeDialogReason
	}
	const ask = "; open the pane to answer it"
	switch key {
	case "enter":
		if cursor == trustOnYes {
			return "the folder trust prompt is up and Enter would choose Yes, I trust this folder" + ask
		}
		return "the folder trust prompt is up and Enter would choose No, exit" + ask
	case "esc":
		return "the folder trust prompt is up and Esc would exit Claude" + ask
	case "ctrl-c":
		return "the folder trust prompt is up and Ctrl-C twice would exit Claude" + ask
	}
	return ""
}

// keystrokePane writes one key to p and says what became of it: delivered
// when the bytes reached the PTY, not delivered (nothing written) for an
// unknown key, a suspended pane (never woken by a key), a pane in
// do-not-disturb (except for the operator and super --urgent, by send's own
// rule, dndOrigin), or one whose process is not running. A dialog never holds
// the operator's key: a key is how a dialog is answered, so it goes into
// whatever is on screen. The one exception is the folder trust prompt, where a
// blind key would end the agent (keystrokeTrustReason). Any other sender's key
// to a pane with a dialog open (the send guard's own test, paneHasModal) is not
// delivered: one agent never answers another's permission prompt. Every agent
// type takes a key the same way; there is no composer to confirm.
func keystrokePane(p *Pane, key string, o sendOrigin) SendVerdict {
	b, ok := keystrokeKeys[key]
	if !ok {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: KeystrokeUnknownReason(key)}
	}
	if p.IsSuspended() {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: p.name + " is suspended; a key does not wake it"}
	}
	// Do-not-disturb by send's own rule; where a send would be held, a key
	// is not delivered: it is about the screen now, so it is never queued.
	var broke bool
	switch r, v := p.dndOrigin(o, &broke); r {
	case dndOriginRefused:
		return v
	case dndOriginWaits:
		age, _ := p.dndAge(time.Now())
		LogInfo("keystroke", "KEY NOT DELIVERED: do-not-disturb", "pane", p.name, "key", key, "from", o.From)
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: keystrokeDNDReason(p.name, age)}
	}
	// sendMu: a key never lands inside a send's paste or between its body
	// and its Enter.
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if p.ptmx == nil {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: p.name + " is not running"}
	}
	if o.From != operatorSender && paneHasModal(p) {
		LogInfo("keystroke", "KEY NOT DELIVERED: dialog open, sender is not the operator", "pane", p.name, "key", key, "from", o.From)
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: keystrokeDialogReason}
	}
	if reason := keystrokeTrustReason(p, key, o); reason != "" {
		LogInfo("keystroke", "KEY NOT DELIVERED: folder trust prompt", "pane", p.name, "key", key, "from", o.From)
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: reason}
	}
	if wrote, err := p.writeInputWait([]byte(b)); err != nil {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: fmt.Sprintf("%s: %v", p.name, err)}
	} else if wrote != childWritten {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: p.name + " is not reading its input"}
	}
	LogInfo("keystroke", "key delivered", "pane", p.name, "key", key, "from", o.From)
	return SendVerdict{Outcome: DeliveryDelivered}
}

// keystrokeResponse is the IPC reply for a key's verdict.
func keystrokeResponse(v SendVerdict) IPCResponse {
	if v.Outcome == DeliveryDelivered {
		return IPCResponse{OK: true, Delivery: DeliveryDelivered}
	}
	return IPCResponse{Error: v.Reason, Delivery: DeliveryNotDelivered}
}

// keystrokeForwarder is a host that can carry a key to an agent another
// initech runs: the TUI, for a peer (host:agent) or another window's pane.
type keystrokeForwarder interface {
	keystrokeHostIsPeer(host string) bool
	forwardKeystroke(conn net.Conn, req IPCRequest, rp *RemotePane)
}

// handleIPCKeystroke is the keystroke action, shared by the TUI and the serve
// daemon: Text is the key name, Target the agent, From and Urgent the sender
// as a send carries them. A bad key name is refused before any lookup.
func handleIPCKeystroke(h IPCHost, conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required", Delivery: DeliveryNotDelivered})
		return
	}
	if !KeystrokeKnown(req.Text) {
		writeIPCResponse(conn, IPCResponse{Error: KeystrokeUnknownReason(req.Text), Delivery: DeliveryNotDelivered})
		return
	}
	fw, canForward := h.(keystrokeForwarder)
	if req.Host != "" && canForward && fw.keystrokeHostIsPeer(req.Host) {
		fw.forwardKeystroke(conn, req, nil)
		return
	}
	pv, ok := h.FindPaneView(req.Target)
	if !ok {
		writeIPCResponse(conn, IPCResponse{Error: "shutting down", Delivery: DeliveryNotDelivered})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target), Delivery: DeliveryNotDelivered})
		return
	}
	switch p := pv.(type) {
	case *Pane:
		writeIPCResponse(conn, keystrokeResponse(keystrokePane(p, req.Text, req.origin())))
	case *RemotePane:
		if canForward {
			fw.forwardKeystroke(conn, req, p)
			return
		}
		writeIPCResponse(conn, IPCResponse{Error: req.Target + " runs in another process", Delivery: DeliveryNotDelivered})
	default:
		writeIPCResponse(conn, IPCResponse{Error: req.Target + " runs in another process", Delivery: DeliveryNotDelivered})
	}
}

// keystrokeHostIsPeer reports whether host names a peer rather than this
// machine.
func (t *TUI) keystrokeHostIsPeer(host string) bool {
	return t.project == nil || host != t.project.PeerName
}

// forwardKeystroke carries the key to the initech that runs the agent, as a
// keystroke control command (never as a send), and answers with its verdict.
// rp is the pane when the caller already has it, else req's host:agent. A
// peer that does not know the action answers "unknown action": older.
func (t *TUI) forwardKeystroke(conn net.Conn, req IPCRequest, rp *RemotePane) {
	if rp == nil {
		var errResp IPCResponse
		if rp, errResp = t.findRemoteTarget(req); rp == nil {
			errResp.Delivery = DeliveryNotDelivered
			writeIPCResponse(conn, errResp)
			return
		}
	}
	resp, err := rp.Request(ControlCmd{Action: "keystroke", Target: rp.Name(), Text: req.Text, From: req.From, Urgent: req.Urgent})
	if reason := peerFailure(resp, err); reason != "" {
		if err == nil && strings.HasPrefix(resp.Error, "unknown action") {
			reason = "its initech is older; restart it for keystroke"
		}
		writeIPCResponse(conn, IPCResponse{Error: reason, Delivery: DeliveryNotDelivered})
		return
	}
	writeIPCResponse(conn, IPCResponse{OK: true, Delivery: DeliveryDelivered})
}

// keystrokeControl is a key from another machine for one of this daemon's
// agents, with the local path's verdict.
func (d *Daemon) keystrokeControl(cmd ControlCmd) ControlResp {
	p := d.findPane(cmd.Target)
	if p == nil {
		return ControlResp{Error: fmt.Sprintf("pane %q not found", cmd.Target), Delivery: DeliveryNotDelivered}
	}
	v := keystrokePane(p, cmd.Text, sendOrigin{From: cmd.From, Urgent: cmd.Urgent})
	if v.Outcome == DeliveryDelivered {
		return ControlResp{OK: true, Delivery: DeliveryDelivered}
	}
	return ControlResp{Error: v.Reason, Delivery: DeliveryNotDelivered}
}
