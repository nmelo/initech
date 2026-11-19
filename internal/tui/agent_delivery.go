package tui

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func (p *Pane) readAgentFrame() agentFrame {
	rows, _ := tryScreenRows(p)
	frame := p.agentReader().read(rows)
	if !frame.box && screenShowsModal(rows) {
		frame.dialog = true
	}
	return frame
}

// holdAgentMessage returns the receipt and queue position from the same lock
// that appends the message. The native queue has already been typed.
func (p *Pane) holdAgentMessage(m QueuedMessage, f agentFrame, reason string) SendVerdict {
	defer p.heldChanged()
	p.mu.Lock()
	defer p.mu.Unlock()
	if limit := p.queueLimitLocked(m); limit != "" {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: queueRefusal(p.name, limit, sendAgainWhenTurnEnds)}
	}
	ahead := len(p.messageQueue) + f.native
	if m.HeldForDialog {
		LogInfo("inject", "deferred: modal open", "pane", p.name, "queued", ahead+1)
	}
	mintReceipt(&m) // a re-sent message keeps its own (ini-yjcy)
	p.messageQueue = append(p.messageQueue, m)
	if m.HeldForDialog {
		name := f.dialogName
		if name == "" {
			name = "modal open"
		}
		return SendVerdict{Outcome: DeliveryHeld, Reason: fmt.Sprintf("held: %s is at a dialog (%s), %d ahead (receipt %s)", p.name, name, ahead, m.Receipt)}
	}
	return SendVerdict{Outcome: DeliveryHeld, Reason: fmt.Sprintf("held for %s: %s, %d ahead (receipt %s)", p.name, reason, ahead, m.Receipt)}
}

func (p *Pane) agentSendLocked(text string, enter bool) SendVerdict {
	f := p.readAgentFrame()
	p.mu.Lock()
	from, backlog := p.sendFrom, len(p.messageQueue) > 0 && !p.dnd
	p.mu.Unlock()
	latched := p.dialogLatched()
	if f.busy && !p.sendAfter && !f.dialog && !latched && (!f.box || f.draft != "") {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: "cannot read an empty composer in the running task; use --after to hold the message"}
	}
	steer := f.busy && !p.sendAfter && f.box && f.draft == "" && !f.dialog
	if (!f.free() && !steer) || (backlog && !steer) || p.dialogLatched() || p.inputWedged() {
		reason := "waiting for an empty input box"
		switch {
		case f.dialog || p.dialogLatched():
			reason = "a dialog is open"
		case p.inputWedged():
			reason = StuckInputDeferral
		case f.busy:
			reason = "queued behind a running task"
		case backlog:
			reason = "waiting for earlier messages"
		}
		return p.holdAgentMessage(QueuedMessage{Text: text, Enter: enter, From: from, Time: time.Now(), HeldForAgent: true, HeldForDialog: f.dialog || p.dialogLatched(), After: p.sendAfter}, f, reason)
	}
	v := p.typeAgentMessage(text, enter, f)
	if v.Outcome == DeliveryDeferred {
		return p.holdAgentMessage(QueuedMessage{Text: text, Enter: enter, Time: time.Now(), From: from}, f, StuckInputDeferral)
	}
	return v
}

func (p *Pane) unknownAgentSend(sent time.Time) SendVerdict {
	rows, _ := tryScreenRows(p)
	cols, height := p.emuSize()
	capture := writeInjectCapture(p.name, cols, height, rows)
	reason := withheldUnknownLine(p.name, sent)
	LogInfo("inject", "submit outcome UNKNOWN", "pane", p.name, "reason", reason, "capture", capture)
	return SendVerdict{Outcome: DeliveryUnconfirmed, Reason: reason}
}

// typeAgentMessage runs with sendMu held. A visible echo permits submission.
// Only a new user turn above a cleared composer confirms that Enter.
func (p *Pane) typeAgentMessage(text string, enter bool, before agentFrame) SendVerdict {
	text, _ = cleanMessage(text) // A suspended or restored queue can predate the send-side cleaner.
	if text == "" {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: "empty message"}
	}
	r := p.agentReader()
	start := time.Now()
	wrote := p.writeBody(bracketedBody(text), "agent-bracketed")
	if wrote != childWritten {
		if wrote == childNotReached {
			return SendVerdict{Outcome: DeliveryDeferred, Reason: StuckInputDeferral}
		}
		return p.unknownAgentSend(start)
	}
	notePerfMarkerWritten(p, text)
	want := strings.TrimSpace(text)
	proofText := newMessageProof(want)
	seenQueued := proofText.queueIndex(r, before.queued) != -1
	seenAbove := seenQueued || proofText.userTurn(r, before.transcript)
	var beforeEnter agentFrame
	deadline := time.Now().Add(submitConfirmWindow)
	for {
		frame := p.readAgentFrame()
		if frame.box && frame.busy == before.busy && !frame.dialog && !p.dialogLatched() && frame.matchesDraft(want) {
			beforeEnter = frame
			seenQueued = seenQueued || proofText.queueIndex(r, frame.queued) != -1
			seenAbove = seenAbove || seenQueued || proofText.userTurn(r, frame.transcript)
			break
		}
		if r == agyReader && frame.dialogName == agySlashMenu && frame.menuFilter == want && !before.busy && !p.dialogLatched() && !p.isClosing() {
			return p.leaveAgyMenu(text, want, start)
		}
		if (frame.box && frame.busy != before.busy) || frame.dialog || p.isClosing() || !time.Now().Before(deadline) {
			return p.unknownAgentSend(start)
		}
		time.Sleep(composerEchoPoll)
	}
	if !enter {
		return SendVerdict{Outcome: DeliveryDelivered}
	}
	pressEnter := func() bool {
		wrote, _ := p.writeInputWait([]byte("\r"))
		return wrote == childWritten
	}
	if !pressEnter() {
		return p.unknownAgentSend(start)
	}
	retried := false
	deadline = time.Now().Add(submitConfirmWindow)
	proofLastSample := false
	nextToolLastSample := false
	for {
		f := p.readAgentFrame()
		if r == agyReader && f.box && f.draft == "" {
			if i := agyQueuedAppend(beforeEnter, f, proofText); i >= 0 {
				return SendVerdict{Outcome: DeliveryQueued, Reason: fmt.Sprintf("queued by %s: runs at the next step of its running task, %d ahead", p.name, i)}
			}
		}
		nextToolProof := false
		if r == codexReader && !seenQueued && f.box && f.draft == "" {
			if i := proofText.queueIndex(r, f.queued); i >= 0 {
				if !f.nextTool {
					return SendVerdict{Outcome: DeliveryQueued, Reason: fmt.Sprintf("queued by %s behind its running task, %d ahead", p.name, i)}
				}
				// The only change to this shelf must be our new, last slot.
				nextToolProof = f.nextToolAppend(beforeEnter, i)
				if nextToolProof && nextToolLastSample {
					return SendVerdict{Outcome: DeliveryQueued, Reason: fmt.Sprintf("queued by %s: runs at the next step of its running task, %d ahead", p.name, i+p.QueueLen())}
				}
			}
		}
		nextToolLastSample = nextToolProof
		proof := f.box && f.draft == "" && !seenAbove && proofText.userTurn(r, f.transcript)
		if proof && proofLastSample {
			if before.busy {
				if !f.busy || r == agyReader {
					return p.unknownAgentSend(start)
				}
				return SendVerdict{Outcome: DeliverySteered, Reason: fmt.Sprintf("steered into %s's running task", p.name)}
			}
			return SendVerdict{Outcome: DeliveryDelivered}
		}
		proofLastSample = proof
		if !time.Now().Before(deadline) {
			if f.box && f.busy == before.busy && !f.dialog && !p.dialogLatched() && f.matchesDraft(want) {
				// Retry only a positive sighting of the unsubmitted draft.
				if r == codexReader && !retried {
					if !pressEnter() {
						return p.unknownAgentSend(start)
					}
					retried = true
					deadline = time.Now().Add(submitConfirmWindow)
					continue
				}
				return SendVerdict{Outcome: DeliveryNotDelivered, Reason: "Enter not taken; the text is still in the composer"}
			}
			return p.unknownAgentSend(start)
		}
		time.Sleep(composerEchoPoll)
	}
}

// agyMenuNotDelivered is the reason a send that opened agy's slash menu
// answers when the menu was closed and our text removed.
const agyMenuNotDelivered = "agy opened its slash menu and the highlighted command was not provably ours"

// leaveAgyMenu runs when our own typed text opened agy's slash menu in a box
// that read idle before typing (a steered send never comes here: Esc during a
// running turn cancels it). Enter is never pressed: the highlighted command
// is not provably ours. One Esc closes the menu, which leaves our text in the
// box; then, only if the box holds exactly that text, one Backspace per
// character typed removes it. A box read empty after that is not delivered;
// anything else is not known, with a capture, and no further key.
func (p *Pane) leaveAgyMenu(text, want string, start time.Time) SendVerdict {
	if wrote, _ := p.writeInputWait([]byte{0x1b}); wrote != childWritten {
		return p.unknownAgentSend(start)
	}
	f, ok := p.awaitAgentFrame(func(f agentFrame) bool { return !f.dialog && f.box })
	if !ok || f.busy || f.draft != want {
		return p.unknownAgentSend(start)
	}
	erase := bytes.Repeat([]byte{0x7f}, utf8.RuneCountInString(text))
	if wrote, _ := p.writeInputWait(erase); wrote != childWritten {
		return p.unknownAgentSend(start)
	}
	if _, ok := p.awaitAgentFrame(agentFrame.free); !ok {
		return p.unknownAgentSend(start)
	}
	LogInfo("inject", "agy slash menu closed and our text removed", "pane", p.name)
	return SendVerdict{Outcome: DeliveryNotDelivered, Reason: agyMenuNotDelivered}
}

// awaitAgentFrame reads the pane until a frame passes want, for at most
// submitConfirmWindow, and returns the last frame read.
func (p *Pane) awaitAgentFrame(want func(agentFrame) bool) (agentFrame, bool) {
	deadline := time.Now().Add(submitConfirmWindow)
	for {
		f := p.readAgentFrame()
		if want(f) {
			return f, true
		}
		if p.isClosing() || !time.Now().Before(deadline) {
			return f, false
		}
		time.Sleep(composerEchoPoll)
	}
}

// Wraps split words or replace inter-word space with a row boundary.
func (f agentFrame) matchesDraft(text string) bool {
	return f.draft != "" && matchesDisplayRows(f.draftRows, text)
}

// drainAgentQueue picks the head only after the reader permits typing.
// Withdrawal and the transition to typing share mu. sendMu keeps a fresh
// sender from overtaking the head or modifying its composer during proof.
func (p *Pane) drainAgentQueue() {
	for {
		p.sendMu.Lock()
		f := p.readAgentFrame()
		if !f.free() || p.dialogLatched() || p.inputWedged() {
			p.sendMu.Unlock()
			return
		}
		p.mu.Lock()
		if !p.alive || p.suspended || p.dnd || len(p.messageQueue) == 0 {
			p.mu.Unlock()
			p.sendMu.Unlock()
			return
		}
		m := p.messageQueue[0]
		p.messageQueue = p.messageQueue[1:]
		p.heldTyping = &m
		p.agentReleaseDone = make(chan struct{})
		p.mu.Unlock()
		p.heldChanged()
		// Saving the typing state can take a filesystem round trip.
		// Recheck the composer before letting any bytes cross the PTY.
		f = p.readAgentFrame()
		p.mu.Lock()
		available := p.alive && !p.suspended && !p.dnd
		p.mu.Unlock()
		v := SendVerdict{Outcome: DeliveryDeferred}
		if available && f.free() && !p.dialogLatched() && !p.inputWedged() {
			v = p.typeAgentMessage(m.Text, m.Enter, f)
		}
		p.mu.Lock()
		close(p.agentReleaseDone)
		p.agentReleaseDone = nil
		p.heldTyping = nil
		if v.Outcome == DeliveryDeferred {
			p.messageQueue = append([]QueuedMessage{m}, p.messageQueue...)
			p.mu.Unlock()
			p.heldChanged()
			p.sendMu.Unlock()
			return
		}
		p.mu.Unlock()
		p.heldChanged()
		p.sendMu.Unlock()
		p.settleReceipt(m, v) // typed, or dropped when refused (ini-yjcy)
		line := "delivered to " + p.name
		if v.Outcome != DeliveryDelivered {
			line = v.Reason
			if v.Outcome == DeliveryNotDelivered {
				line = "not delivered to " + p.name + ": " + v.Reason
			}
		}
		p.reportEvent(AgentEvent{Type: EventMessageSent, Pane: p.name, Detail: line, From: m.From, ToSender: line, Time: time.Now()})
		if v.Outcome != DeliveryDelivered || p.QueueLen() == 0 {
			return
		}
		time.Sleep(queueDrainInterval)
	}
}

// sameSender reports two stamps from one sender. The operator and a send
// from outside a pane are one person for a withdrawal: a receipt held before
// an upgrade was stamped as the operator, after it outside a pane (ini-slqv).
func sameSender(a, b string) bool {
	outside := func(s string) bool { return s == operatorSender || s == outsideSender }
	return a == b || (outside(a) && outside(b))
}

func (p *Pane) withdrawReceipt(id, from string) (string, error) {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, "\r\n") {
		return "", fmt.Errorf("a withdrawal receipt is required on one line")
	}
	p.mu.Lock()
	// A write that never reaches the child restores this receipt to the queue.
	// Wait only for the writer of this receipt to decide its outcome.
	for p.heldTyping != nil && p.heldTyping.Receipt == id && p.agentReleaseDone != nil {
		done := p.agentReleaseDone
		p.mu.Unlock()
		<-done
		p.mu.Lock()
	}
	for i, m := range p.messageQueue {
		if m.Receipt != id {
			continue
		}
		if m.From != "" && !sameSender(m.From, from) {
			p.mu.Unlock()
			return "", fmt.Errorf("receipt %s belongs to another sender", id)
		}
		p.messageQueue = append(p.messageQueue[:i], p.messageQueue[i+1:]...)
		p.mu.Unlock()
		p.heldChanged()
		p.noteReceipt(m, ReceiptWithdrawn)
		return "withdrawn: " + id, nil
	}
	p.mu.Unlock()
	r := p.book().state(p.name, id)
	if r.State == ReceiptTyped {
		if owner := r.From; owner != "" && !sameSender(owner, from) {
			return "", fmt.Errorf("receipt %s belongs to another sender", id)
		}
		return "", fmt.Errorf("receipt %s was already typed", id)
	}
	return "", fmt.Errorf("unknown receipt %s", id)
}

func newAgentReceipt() string { return rand.Text() }

func (p *Pane) receiptReply(id, from string) IPCResponse {
	line, err := p.withdrawReceipt(id, from)
	if err != nil {
		return IPCResponse{Error: err.Error()}
	}
	return IPCResponse{OK: true, Data: line, Delivery: "withdrawn"}
}

func (p *Pane) queueForWake(text string, enter bool, o sendOrigin) SendVerdict {
	m := QueuedMessage{Text: text, Enter: enter, From: o.From, Time: time.Now(), Receipt: o.Receipt}
	if p.agentReader() != noAgentReader {
		return p.holdAgentMessage(m, agentFrame{}, "waiting for the agent to wake")
	}
	why, receipt := p.enqueueReceipt(m, sendAgainWhenResumes)
	if why != "" {
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: why}
	}
	// Delivered, as before, for the wake delivers it; the receipt tells a
	// drain re-sending it that it is held again (ini-yjcy).
	return SendVerdict{Outcome: DeliveryDelivered, Receipt: receipt}
}
