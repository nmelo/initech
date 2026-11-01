package tui

import (
	"testing"
	"time"
)

// ini-5qf: the pane's stored bead is the id only; the title is display only.

// commaTitle is ini-y0n.2's real bd title: its commas split the text that
// initech bead sends ("id<TAB>title") into comma "ids" before the fix.
const commaTitle = "Progress step 2: a pane header shows its effort pace (closed/total, per hour, stuck)"

// AC1: initech bead ini-y0n.2 sends "ini-y0n.2<TAB><title>"; the ribbon
// value, the list reply's beads (status --json, the push) and the record's
// bead are "ini-y0n.2" and nothing more, and the title shows for display.
// Red at base: the comma path stored "ini-y0n.2<TAB>Progress ... (closed/total".
func TestBeadStore_IDWithACommaTitleStoresTheIDOnly(t *testing.T) {
	p := newEmuPane("eng4", 80, 24)
	sock := noBeadSocket(t, p)
	if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng4", Text: "ini-y0n.2\t" + commaTitle}); !r.OK {
		t.Fatalf("bead: %s", r.Error)
	}
	if ids := p.BeadIDs(); len(ids) != 1 || ids[0] != "ini-y0n.2" {
		t.Errorf("stored beads %q, want exactly [ini-y0n.2]", ids)
	}
	if e := listPane(t, sock, "eng4"); string(e["beads"]) != `["ini-y0n.2"]` {
		t.Errorf("list beads %s, want [\"ini-y0n.2\"]", e["beads"])
	}
	if w := p.sessionWork(); w.bead != "ini-y0n.2" || w.source != "manual" {
		t.Errorf("record work bead %q source %q, want ini-y0n.2 manual", w.bead, w.source)
	}
	if got := p.BeadTitle(); got != commaTitle {
		t.Errorf("display title %q, want the bd title", got)
	}
	p.mu.Lock()
	named := p.lastNamed
	p.mu.Unlock()
	if named != "ini-y0n.2" {
		t.Errorf("last-named %q, want ini-y0n.2", named)
	}
}

// The comma form still names several ids, each one stored as given.
func TestBeadStore_CommaListStillNamesSeveral(t *testing.T) {
	p := newEmuPane("eng4", 80, 24)
	sock := noBeadSocket(t, p)
	if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng4", Text: "ini-a, ini-b.2"}); !r.OK {
		t.Fatalf("bead: %s", r.Error)
	}
	if ids := p.BeadIDs(); len(ids) != 2 || ids[0] != "ini-a" || ids[1] != "ini-b.2" {
		t.Errorf("stored %q, want [ini-a ini-b.2]", ids)
	}
}

// The id shape: prefix, dash, key, an optional dotted child; molecule ids
// with more dashes pass.
func TestBeadID_Shape(t *testing.T) {
	for s, want := range map[string]bool{
		"ini-y0n.2": true, "ini-5qf": true, "hov-mol-ox6": true, "ini-a.1.2": true,
		"": false, " ": false, "not an id": false, "ini": false, "ini-": false, "-abc": false,
		"ini-a.": false, "ini-a.x": false, "ini-a\tTitle": false, "ini-a,ini-b": false, " ini-a": false,
	} {
		if got := IsBeadID(s); got != want {
			t.Errorf("IsBeadID(%q) = %v, want %v", s, got, want)
		}
	}
	for s, want := range map[string]string{
		"ini-y0n.2\t" + commaTitle: "ini-y0n.2", "ini-a": "ini-a",
		"\tTitle": "", "garbage here\tTitle": "", "per hour": "", "": "",
	} {
		if got := CutBeadID(s); got != want {
			t.Errorf("CutBeadID(%q) = %q, want %q", s, got, want)
		}
	}
}

// The bead action refuses anything that is not ids, and changes nothing;
// a clear still clears.
func TestBeadStore_HandlerRefusesANonIDAndKeepsTheBead(t *testing.T) {
	p := newEmuPane("eng4", 80, 24)
	p.SetBead("ini-keep", "")
	sock := noBeadSocket(t, p)
	for _, text := range []string{"not an id", "\tTitle only", "ini-a, bad id", " \t "} {
		if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng4", Text: text}); r.OK {
			t.Errorf("bead %q accepted, want refused", text)
		}
		if ids := p.BeadIDs(); len(ids) != 1 || ids[0] != "ini-keep" {
			t.Errorf("after refusing %q the stored bead is %q, want ini-keep unchanged", text, ids)
		}
	}
	if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng4", Text: ""}); !r.OK {
		t.Fatalf("clear refused: %s", r.Error)
	}
	if ids := p.BeadIDs(); len(ids) != 0 {
		t.Errorf("after --clear: %q, want none", ids)
	}
}

// AC4, in memory: a pane holding a non-id override from before the fix is
// cleaned at the next poll: cut to its id when it has one, a field with no
// id dropped. The record then closes the bad interval and opens one on the
// id.
func TestBeadPoll_CleansANonIDOverrideAndTheRecordFollows(t *testing.T) {
	r, root := sbRecorder(t)
	p := sbPane("eng4", sbBase, sbBase.Add(time.Second))
	bad := "ini-y0n.2\tProgress step 2: a pane header shows its effort pace (closed/total"
	p.mu.Lock()
	p.beadIDs = []string{bad, "per hour", "stuck)"}
	p.lastNamed = bad
	p.overrideSince = sbBase.Add(2 * time.Second)
	p.mu.Unlock()
	r.reconcile([]paneWork{p.sessionWork()}, sbBase.Add(time.Minute))

	p.applyDerivedBeads([]bdBead{bdAt("ini-y0n.2", "eng4", sbBase)}, true, sbBase.Add(2*time.Minute))
	if ids := p.BeadIDs(); len(ids) != 1 || ids[0] != "ini-y0n.2" {
		t.Fatalf("after the poll the override is %q, want [ini-y0n.2]", ids)
	}
	p.mu.Lock()
	named := p.lastNamed
	p.mu.Unlock()
	if named != "ini-y0n.2" {
		t.Errorf("last-named %q, want ini-y0n.2", named)
	}
	r.reconcile([]paneWork{p.sessionWork()}, sbBase.Add(2*time.Minute))
	lines := sbParsed(t, root)
	last := lines[len(lines)-1]
	if sbStr(last.Bead) != "ini-y0n.2" || last.To != nil || last.Source != "manual" {
		t.Errorf("the last line is bead %s to %s source %s, want an open ini-y0n.2 manual interval", sbStr(last.Bead), sbStr(last.To), last.Source)
	}
}

// A non-id override with no id in it is dropped, and the pane is back on bd.
func TestBeadPoll_DropsAnOverrideWithNoID(t *testing.T) {
	p := newEmuPane("eng4", 80, 24)
	p.mu.Lock()
	p.beadIDs = []string{"per hour"}
	p.mu.Unlock()
	p.applyDerivedBeads([]bdBead{bdAt("ini-d", "eng4", t0)}, true, t0)
	if ids := p.BeadIDs(); len(ids) != 1 || ids[0] != "ini-d" {
		t.Errorf("after the poll: %q, want the derived ini-d", ids)
	}
}
