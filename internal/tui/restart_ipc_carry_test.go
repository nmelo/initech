package tui

import (
	"net"
	"strings"
	"testing"
	"time"
)

// ini-f6kg: `initech restart` (the IPC restart) carries the messages held for
// the agent to the new process through the same queue_carry.go helpers the
// command-bar restart uses. Before this it closed the old pane and dropped
// its queue: the pane left the list, so nothing ever reported the mail.

// f6kgCatPane is a live pane running /bin/cat with n messages held in its
// queue, as a dialog or a park would leave them.
func f6kgCatPane(t *testing.T, texts ...string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{Name: "eng2", Command: []string{"/bin/cat"}}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.Start()
	t.Cleanup(p.Close)
	for _, s := range texts {
		p.EnqueueMessage(s, true)
	}
	return p
}

func f6kgTUI(panes ...PaneView) *TUI {
	return &TUI{panes: panes, agentEvents: make(chan AgentEvent, 16), quitCh: make(chan struct{})}
}

func f6kgIPCRestart(t *testing.T, tui *TUI) IPCResponse {
	t.Helper()
	return i35wIPC(t, func(c net.Conn) { tui.handleIPCRestart(c, IPCRequest{Target: "eng2"}) })
}

// f6kgNew is the pane now in slot 0, which must be a new one.
func f6kgNew(t *testing.T, tui *TUI, old *Pane) *Pane {
	t.Helper()
	np, ok := tui.panes[0].(*Pane)
	if !ok || np == old {
		t.Fatal("the restart did not put a new local pane in the slot")
	}
	t.Cleanup(np.Close)
	return np
}

func f6kgTexts(msgs []QueuedMessage) string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return strings.Join(out, "|")
}

// Criterion 1: two held messages survive `initech restart`, in order, on the
// new pane; the old pane keeps none. Before this the new queue was empty.
func TestRestartIPC_HeldMessagesAreCarriedInOrder(t *testing.T) {
	old := f6kgCatPane(t, "[from super] first", "[from pm] second")
	tui := f6kgTUI(old)
	if resp := f6kgIPCRestart(t, tui); !resp.OK {
		t.Fatalf("restart: %+v", resp)
	}
	np := f6kgNew(t, tui, old)
	if got := f6kgTexts(np.DrainQueue()); got != "[from super] first|[from pm] second" {
		t.Fatalf("the restarted pane holds %q, want both messages in order", got)
	}
	if n := old.QueuedMessageCount(); n != 0 {
		t.Fatalf("the old pane still holds %d: the mail must move, not be copied", n)
	}
}

// Criterion 1, delivered: the carried messages reach the new process once it
// is up, by the new pane's normal path.
func TestRestartIPC_CarriedMessagesReachTheNewProcess(t *testing.T) {
	old, err := NewPane(PaneConfig{Name: "eng2", Command: []string{"sh"}, NoBracketedPaste: true}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	old.Start()
	t.Cleanup(old.Close)
	old.EnqueueMessage("echo F6KG_CARRIED_ONE", true)
	old.EnqueueMessage("echo F6KG_CARRIED_TWO", true)
	tui := f6kgTUI(old)
	if resp := f6kgIPCRestart(t, tui); !resp.OK {
		t.Fatalf("restart: %+v", resp)
	}
	np := f6kgNew(t, tui, old)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		screen := np.emu.Render()
		if strings.Contains(screen, "F6KG_CARRIED_ONE") && strings.Contains(screen, "F6KG_CARRIED_TWO") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the carried messages never reached the restarted process (queue %d):\n%s",
		np.QueuedMessageCount(), np.emu.Render())
}

// Criterion 2: the new pane cannot start. The old pane keeps its queue, the
// handler answers an error, and the failure is reported.
func TestRestartIPC_ASpawnFailureKeepsTheMailAndAnswersAnError(t *testing.T) {
	old := f6kgCatPane(t, "[from super] keep me", "[from pm] me too")
	tui := f6kgTUI(old)
	// A missing binary is not a spawn failure: the command runs as
	// `$SHELL -l -c "exec ..."`, so the shell starts and the exec fails inside
	// the new pane. A working directory that does not exist fails the start
	// itself, which is the branch this cell is for.
	tui.paneConfigBuilder = func(string) (PaneConfig, error) {
		return PaneConfig{Name: "eng2", Command: []string{"/bin/cat"}, Dir: "/nonexistent/initech-f6kg-no-such-dir"}, nil
	}
	logs := captureLogs(t)
	resp := f6kgIPCRestart(t, tui)
	if resp.OK || !strings.Contains(resp.Error, "restart failed") {
		t.Fatalf("restart with an unspawnable command: %+v, want an error", resp)
	}
	if got := f6kgTexts(old.DrainQueue()); got != "[from super] keep me|[from pm] me too" {
		t.Fatalf("after a failed restart the old pane holds %q, want both messages", got)
	}
	if !strings.Contains(logs.String(), "messages kept on the old pane") {
		t.Error("a failed restart holding mail left no warning")
	}
}

// Criterion 3: a parked agent's resume mail is carried, not dropped.
func TestRestartIPC_AParkedAgentsMailIsCarried(t *testing.T) {
	old := f6kgCatPane(t)
	parkPaneSuspended(old)
	old.EnqueueMessage("[from super] wake up", true)
	tui := f6kgTUI(old)
	if resp := f6kgIPCRestart(t, tui); !resp.OK {
		t.Fatalf("restart of a parked agent: %+v", resp)
	}
	np := f6kgNew(t, tui, old)
	if got := f6kgTexts(np.DrainQueue()); got != "[from super] wake up" {
		t.Fatalf("the restarted pane holds %q, want the parked agent's mail", got)
	}
}

// Criterion 4: both restarts, the command bar's and `initech restart`, carry
// the same way. One table, the same assertions over both entry points.
func TestRestart_BothEntryPointsCarryTheSameWay(t *testing.T) {
	for name, restart := range map[string]func(*testing.T, *TUI, *Pane){
		"command bar": func(t *testing.T, tui *TUI, p *Pane) {
			if err := tui.restartPane(p); err != nil {
				t.Fatalf("restartPane: %v", err)
			}
		},
		"initech restart": func(t *testing.T, tui *TUI, p *Pane) {
			if resp := f6kgIPCRestart(t, tui); !resp.OK {
				t.Fatalf("handleIPCRestart: %+v", resp)
			}
		},
	} {
		old := f6kgCatPane(t, "one", "two", "three")
		tui := f6kgTUI(old)
		restart(t, tui, old)
		np := f6kgNew(t, tui, old)
		if got := f6kgTexts(np.DrainQueue()); got != "one|two|three" {
			t.Errorf("%s: the new pane holds %q, want one|two|three", name, got)
		}
	}
}

// Criterion 5: the reply and the event name the restart and how many
// messages were carried, matching the queue.
func TestRestartIPC_TheReplyAndEventCountTheCarriedMail(t *testing.T) {
	old := f6kgCatPane(t, "a", "b", "c")
	tui := f6kgTUI(old)
	resp := f6kgIPCRestart(t, tui)
	f6kgNew(t, tui, old)
	if !resp.OK || !strings.Contains(resp.Data, "carried 3 held message") {
		t.Fatalf("reply %+v, want OK naming 3 carried messages", resp)
	}
	select {
	case ev := <-tui.agentEvents:
		if ev.Type != EventAgentRestarted || !strings.Contains(ev.Detail, "Restarted eng2") || !strings.Contains(ev.Detail, "carried 3") {
			t.Fatalf("event %v %q, want Restarted eng2 with carried 3", ev.Type, ev.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("no restart event")
	}
}

// Feed it nothing: an empty queue carries nothing and says nothing about it;
// a zero-length message is carried as a message.
func TestRestartIPC_NothingToCarry(t *testing.T) {
	empty := f6kgCatPane(t)
	tui := f6kgTUI(empty)
	resp := f6kgIPCRestart(t, tui)
	np := f6kgNew(t, tui, empty)
	if !resp.OK || strings.Contains(resp.Data, "carried") || np.QueuedMessageCount() != 0 {
		t.Fatalf("empty queue: reply %+v, new queue %d; want OK, no count, nothing queued", resp, np.QueuedMessageCount())
	}

	blank := f6kgCatPane(t, "")
	tui2 := f6kgTUI(blank)
	resp2 := f6kgIPCRestart(t, tui2)
	np2 := f6kgNew(t, tui2, blank)
	if !resp2.OK || np2.QueuedMessageCount() != 1 {
		t.Fatalf("one zero-length message: reply %+v, new queue %d; want it carried", resp2, np2.QueuedMessageCount())
	}
}

// The pane list changed while the restart was building the new pane: the new
// pane is not put in, the mail goes back to the old pane, and the reply is an
// error, not OK for a restart that did not happen.
func TestRestartIPC_AListChangeMidRestartKeepsTheMail(t *testing.T) {
	old := f6kgCatPane(t, "[from super] hold", "[from pm] these")
	other := testPane("qa1")
	tui := f6kgTUI(old)
	// The builder runs between finding the pane and swapping the new one in:
	// a pane added in front stands for any change to the list in that window.
	tui.paneConfigBuilder = func(string) (PaneConfig, error) {
		tui.panes = append([]PaneView{other}, tui.panes...)
		return PaneConfig{Name: "eng2", Command: []string{"/bin/cat"}}, nil
	}
	resp := f6kgIPCRestart(t, tui)
	if resp.OK {
		t.Fatalf("a restart that replaced nothing answered OK: %+v", resp)
	}
	if got := f6kgTexts(old.DrainQueue()); got != "[from super] hold|[from pm] these" {
		t.Fatalf("after the list changed the old pane holds %q, want both messages back", got)
	}
}
