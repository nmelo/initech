package tui

import (
	"testing"
	"time"
)

// ini-8xf: initech bead --clear applies the last-named tie rule at once,
// from the last answer bd gave, not at the next poll.

// twoBeads is bd's answer for a role holding two in_progress beads: ini-old
// updated first, ini-new later, so the newest rule alone picks ini-new.
func twoBeads(role string) []bdBead {
	return []bdBead{bdAt("ini-old", role, t0), bdAt("ini-new", role, t0.Add(time.Minute))}
}

// AC1: the older bead named, then --clear: the pane shows the older bead at
// once, never the newer. Red at base: the newer until the next poll.
func TestBeadClear_TwoBeadsReturnsToTheLastNamedAtOnce(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.applyDerivedBeads(twoBeads("eng1"), true, t0)
	if p.BeadID() != "ini-new" {
		t.Fatalf("fixture: derived %q, want the newest ini-new before anything is named", p.BeadID())
	}
	p.SetBead("ini-old", "")
	p.SetBead("", "")
	if got := p.BeadID(); got != "ini-old" {
		t.Errorf("straight after --clear the pane shows %q, want the last-named ini-old", got)
	}

	// The same through SetBeads, the multi-bead path.
	q := newEmuPane("eng2", 80, 24)
	q.applyDerivedBeads(twoBeads("eng2"), true, t0)
	q.SetBeads([]string{"ini-old"})
	q.SetBeads(nil)
	if got := q.BeadID(); got != "ini-old" {
		t.Errorf("SetBeads(nil) after naming ini-old: pane shows %q, want ini-old", got)
	}
}

// AC2: one bead: --clear returns to it; no bead: --clear returns to none.
func TestBeadClear_OneBeadOrNone(t *testing.T) {
	one := newEmuPane("eng1", 80, 24)
	one.applyDerivedBeads([]bdBead{bdAt("ini-a", "eng1", t0)}, true, t0)
	one.SetBead("ini-x", "")
	one.SetBead("", "")
	if got := one.BeadID(); got != "ini-a" {
		t.Errorf("one bead in bd, --clear: pane shows %q, want ini-a", got)
	}

	none := newEmuPane("eng1", 80, 24)
	none.applyDerivedBeads(nil, true, t0)
	none.SetBead("ini-x", "")
	none.SetBead("", "")
	if got := none.BeadID(); got != "" {
		t.Errorf("no bead in bd, --clear: pane shows %q, want none", got)
	}
}

// AC3 (the record is not on main yet): the derived value Step 2A reads
// changes once at the --clear, and the next poll with the same answer
// changes nothing, neither the bead nor when it began.
func TestBeadClear_DerivedValueChangesOnceAtTheClear(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.applyDerivedBeads(twoBeads("eng1"), true, t0)
	p.SetBead("ini-old", "")
	p.SetBead("", "")
	id, _, since := p.DerivedBeadSince()
	if id != "ini-old" {
		t.Fatalf("after --clear the derived bead is %q, want ini-old", id)
	}
	p.applyDerivedBeads(twoBeads("eng1"), true, t0.Add(10*time.Second))
	id2, _, since2 := p.DerivedBeadSince()
	if id2 != id || !since2.Equal(since) {
		t.Errorf("the next poll moved the derived value: %q since %v, then %q since %v; want one change, at the clear", id, since, id2, since2)
	}
}

// AC4 feed-it-nothing: --clear with nothing named keeps bd's newest; --clear
// while bd is unreachable keeps the stale value; --clear before any poll
// answered (a fresh or restarted pane) keeps what the pane has.
func TestBeadClear_NothingNamedStaleOrNeverPolledKeepTheValue(t *testing.T) {
	plain := newEmuPane("eng1", 80, 24)
	plain.applyDerivedBeads(twoBeads("eng1"), true, t0)
	plain.SetBead("", "")
	if got := plain.BeadID(); got != "ini-new" {
		t.Errorf("nothing named, --clear: pane shows %q, want bd's newest ini-new", got)
	}

	// Two beads, so re-deriving (ini-old, last named) and keeping the stale
	// value (ini-new) differ.
	stale := newEmuPane("eng1", 80, 24)
	stale.applyDerivedBeads(twoBeads("eng1"), true, t0)
	stale.applyDerivedBeads(nil, false, t0.Add(10*time.Second))
	stale.SetBead("ini-old", "")
	stale.SetBead("", "")
	if got := stale.BeadID(); got != "ini-new" || !stale.BeadStale() {
		t.Errorf("bd unreachable, --clear: pane shows %q stale=%v, want the stale ini-new still marked", got, stale.BeadStale())
	}

	fresh := newEmuPane("eng1", 80, 24)
	fresh.mu.Lock()
	fresh.derivedBead, fresh.derivedTitle = "ini-carried", "carried by a restart"
	fresh.mu.Unlock()
	fresh.SetBead("ini-x", "")
	fresh.SetBead("", "")
	if got := fresh.BeadID(); got != "ini-carried" {
		t.Errorf("no poll answered yet, --clear: pane shows %q, want the value it had, ini-carried", got)
	}
}

// Reachability: the IPC bead action with empty text, what initech bead
// --clear sends, applies the tie rule before its reply; the list reply that
// status and the other windows read leads with the last-named bead.
func TestBeadClear_OverTheSocketListLeadsWithTheLastNamed(t *testing.T) {
	p := newEmuPane("eng3", 80, 24)
	p.applyDerivedBeads(twoBeads("eng3"), true, t0)
	sock := noBeadSocket(t, p)
	if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng3", Text: "ini-old"}); !r.OK {
		t.Fatalf("bead: %s", r.Error)
	}
	if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng3", Text: ""}); !r.OK {
		t.Fatalf("clear: %s", r.Error)
	}
	if e := listPane(t, sock, "eng3"); string(e["beads"]) != `["ini-old"]` {
		t.Errorf("list beads straight after --clear = %s, want [\"ini-old\"]", e["beads"])
	}
}

// A woken pane carries bd's last answer, so a --clear straight after the
// wake, before its first poll, still applies the tie rule.
func TestBeadClear_WokenPaneCarriesTheLastAnswer(t *testing.T) {
	p := newEmuPane("eng3", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	p.applyDerivedBeads(twoBeads("eng3"), true, t0)
	p.SetBead("ini-old", "")
	tu := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}
	if err := tu.resumePane(p, "test"); err != nil {
		t.Fatalf("resumePane: %v", err)
	}
	np := tu.panes[0].(*Pane)
	t.Cleanup(func() { np.Close() })
	if np == p {
		t.Fatal("resume did not replace the pane; the carry was not exercised")
	}
	np.SetBead("", "")
	if got := np.BeadID(); got != "ini-old" {
		t.Errorf("woken pane, --clear before its first poll: shows %q, want the last-named ini-old", got)
	}
}
