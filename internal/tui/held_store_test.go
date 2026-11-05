package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// ini-8rkg: held mail survives a window 1 restart, or is said when it cannot.

// heldWindow is one window 1 lifetime for a test: a writer on root and one
// pane, eng1, that records what is typed into it.
type heldWindow struct {
	t     *TUI
	p     *Pane
	typed func() string
}

func startHeldWindow(t *testing.T, root string, panes ...*Pane) *heldWindow {
	t.Helper()
	p, _, typed := dndPane(t, "eng1")
	tu := newTestTUI(append([]*Pane{p}, panes...)...)
	tu.projectRoot = root
	tu.held = newHeldWriter(root, heldFileWindowOne)
	for _, pv := range tu.panes {
		if lp, ok := pv.(*Pane); ok {
			lp.setHeldHook(tu.held)
		}
	}
	return &heldWindow{t: tu, p: p, typed: typed}
}

// restart is a window 1 that died without a graceful exit (nothing below
// runs any exit step) followed by a fresh one on the same root.
func restartHeldWindow(t *testing.T, root string, beforeLoad func(*Pane), panes ...*Pane) *heldWindow {
	t.Helper()
	w := startHeldWindow(t, root, panes...)
	if beforeLoad != nil {
		beforeLoad(w.p)
	}
	w.t.loadHeldMail(time.Now())
	return w
}

func queueTexts(p *Pane) []string {
	var out []string
	for _, m := range dndQueue(p) {
		out = append(out, m.Text)
	}
	return out
}

// TestHeldMail_HeldMailAcrossAWindowOneRestart is ini-pt3j's scratch read,
// landed: two held sends of each kind, window 1 gone without an exit step,
// then a fresh one; both are back and delivered in order.
func TestHeldFile_HeldMailAcrossAWindowOneRestart(t *testing.T) {
	for _, kind := range []string{heldKindDND, heldKindSuspended, heldKindDialog} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			old := startHeldWindow(t, root)
			old.t.held.start()
			hold := func(text string) {
				switch kind {
				case heldKindDND:
					if v := old.p.SendTextVerdictFrom(text, true, sendOrigin{From: "eng2"}); v.Outcome != DeliveryHeld {
						t.Fatalf("send: %+v, want held", v)
					}
				case heldKindSuspended:
					parkForTest(old.p)
					old.p.SendTextFrom(text, true, sendOrigin{From: "eng2"})
				case heldKindDialog:
					old.p.setSendFrom("eng2")
					old.p.enqueueHeld(text, true)
					old.p.setSendFrom("")
				}
			}
			if kind == heldKindDND {
				dndSet(t, old.t, "eng1", "on")
			}
			hold("[from eng2] HELD-ONE")
			hold("[from eng2] HELD-TWO")
			if n := old.p.QueueLen(); n != 2 {
				t.Fatalf("held before the restart: %d, want 2", n)
			}

			var before func(*Pane)
			if kind == heldKindSuspended {
				before = parkForTest
			}
			fresh := restartHeldWindow(t, root, before)
			got := queueTexts(fresh.p)
			if kind == heldKindSuspended {
				// Stays queued for a suspended pane: shown by waking it.
				fresh.p.mu.Lock()
				fresh.p.suspended, fresh.p.alive = false, true
				fresh.p.mu.Unlock()
				fresh.p.startModalDrain()
			}
			if !waitHeld(func() bool { return strings.Contains(fresh.typed(), "HELD-TWO") }) {
				t.Fatalf("after the restart: queue %q, typed %q; want both delivered", got, fresh.typed())
			}
			typed := fresh.typed()
			if i, j := strings.Index(typed, "HELD-ONE"), strings.Index(typed, "HELD-TWO"); i < 0 || j < i {
				t.Fatalf("delivered out of order: %q", typed)
			}
			if !waitHeld(func() bool {
				_, err := os.Stat(filepath.Join(root, ".initech", heldFileWindowOne))
				return os.IsNotExist(err)
			}) {
				t.Errorf("the held file is still there after the last delivery")
			}
		})
	}
}

func waitHeld(cond func() bool) bool {
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// writeHeld puts a held file on root as an older window 1 left it.
func writeHeld(t *testing.T, root string, entries ...heldEntry) {
	t.Helper()
	w := newHeldWriter(root, heldFileWindowOne)
	w.ready = true
	w.panes[&Pane{}] = entries
	if err := w.writeLocked(); err != nil {
		t.Fatal(err)
	}
}

var heldT0 = time.Date(2026, 11, 5, 15, 30, 0, 0, time.UTC)

// AC3: an entry marked typing is never typed again; its sender is told.
func TestHeldFile_ATypingEntryIsNotRetypedAndItsSenderIsTold(t *testing.T) {
	lines := recordInitechLines(t)
	root := t.TempDir()
	writeHeld(t, root,
		heldEntry{Pane: "eng1", Text: "TYPING-ONE", Enter: true, Time: heldT0, Kind: heldKindDND, From: "eng2", State: heldStateTyping},
		heldEntry{Pane: "eng1", Text: "HELD-AFTER", Enter: true, Time: heldT0.Add(time.Second), Kind: heldKindSuspended, From: "eng2", State: heldStateHeld})
	eng2, _, _ := dndPane(t, "eng2")
	w := restartHeldWindow(t, root, func(p *Pane) { parkForTest(p) }, eng2)
	if q := queueTexts(w.p); len(q) != 1 || q[0] != "HELD-AFTER" {
		t.Fatalf("queue %q, want only HELD-AFTER (the typing entry never retyped)", q)
	}
	got := strings.Join(lines.waitFor(1), "\n")
	if !strings.Contains(got, "eng2|initech|") || !strings.Contains(got, "may or may not have arrived") {
		t.Fatalf("lines %q, want eng2 told the typing message may or may not have arrived", got)
	}
}

// AC4 6(a): a pane that is gone at start; its sender is told.
func TestHeldFile_AGonePaneIsSaidToTheSender(t *testing.T) {
	lines := recordInitechLines(t)
	root := t.TempDir()
	writeHeld(t, root, heldEntry{Pane: "ghost", Text: "TO-GHOST", Enter: true, Time: heldT0, Kind: heldKindDialog, From: "eng2", State: heldStateHeld})
	eng2, _, _ := dndPane(t, "eng2")
	restartHeldWindow(t, root, nil, eng2)
	got := strings.Join(lines.waitFor(1), "\n")
	if !strings.Contains(got, "eng2|initech|") || !strings.Contains(got, "held message to ghost since 15:30 was not delivered: ghost no longer exists") {
		t.Fatalf("lines %q, want eng2 told ghost no longer exists", got)
	}
}

// AC4 6(b): a file that does not parse, or has another version, is set
// aside (never deleted) and super is told.
func TestHeldFile_ABadFileIsSetAsideAndSuperIsTold(t *testing.T) {
	for name, body := range map[string]string{"garbage": "held: [unclosed", "version 2": "version: 2\nheld: []\n"} {
		t.Run(name, func(t *testing.T) {
			lines := recordInitechLines(t)
			root := t.TempDir()
			path := filepath.Join(root, ".initech", heldFileWindowOne)
			os.MkdirAll(filepath.Dir(path), 0o700)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			super, _, _ := dndPane(t, "super")
			restartHeldWindow(t, root, nil, super)
			aside, _ := filepath.Glob(path + ".bad-*")
			if len(aside) != 1 {
				t.Fatalf("set aside: %v, want one %s.bad-<time>", aside, heldFileWindowOne)
			}
			if b, _ := os.ReadFile(aside[0]); string(b) != body {
				t.Fatalf("the set-aside file holds %q, want the original", b)
			}
			got := strings.Join(lines.waitFor(1), "\n")
			if !strings.Contains(got, "super|initech|") || !strings.Contains(got, "did not parse and was set aside") {
				t.Fatalf("lines %q, want super told", got)
			}
		})
	}
}

// AC4 6(d): more held at start than a queue takes: the oldest kept, the
// rest said to the sender.
func TestHeldFile_AFullQueueAtStartKeepsTheOldestAndSaysTheRest(t *testing.T) {
	lines := recordInitechLines(t)
	root := t.TempDir()
	var es []heldEntry
	for i := 0; i < maxMessageQueue+2; i++ {
		es = append(es, heldEntry{Pane: "eng1", Text: "M" + itoa(i), Enter: true, Time: heldT0.Add(time.Duration(i) * time.Second), Kind: heldKindSuspended, From: "eng2", State: heldStateHeld})
	}
	writeHeld(t, root, es...)
	eng2, _, _ := dndPane(t, "eng2")
	w := restartHeldWindow(t, root, func(p *Pane) { parkForTest(p) }, eng2)
	q := queueTexts(w.p)
	if len(q) != maxMessageQueue || q[0] != "M0" || q[len(q)-1] != "M"+itoa(maxMessageQueue-1) {
		t.Fatalf("queue %v, want M0..M%d", q, maxMessageQueue-1)
	}
	if got := lines.waitFor(2); len(got) != 2 || !strings.Contains(got[0], "its queue is full") {
		t.Fatalf("lines %q, want two queue-full lines to eng2", got)
	}
}

// AC4 6(c): a write that fails: at exit, one line per pane with held mail.
func TestHeldFile_AFailedWriteIsSaidAtExit(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, ".initech")
	if err := os.WriteFile(blocker, []byte("a file where the folder goes"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := startHeldWindow(t, root)
	w.t.held.start()
	parkForTest(w.p)
	w.p.SendTextFrom("UNKEPT", true, sendOrigin{From: "eng2"})
	got := w.t.held.unkept()
	if len(got) != 1 || !strings.Contains(got[0], "1 held message(s) to eng1 (from [eng2]) could not be kept") {
		t.Fatalf("exit lines %q, want one naming eng1, the count and eng2", got)
	}
}

// AC6: the file is 0600, replaced by rename (a new file each write, never
// rewritten in place), and the log never carries a message's text.
func TestHeldFile_TheFileIs0600AtomicAndNeverLogged(t *testing.T) {
	logs := captureLogs(t)
	root := t.TempDir()
	w := startHeldWindow(t, root)
	w.t.held.start()
	parkForTest(w.p)
	w.p.SendTextFrom("SECRET-ONE", true, sendOrigin{From: "eng2"})
	path := filepath.Join(root, ".initech", heldFileWindowOne)
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st1.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", st1.Mode().Perm())
	}
	w.p.SendTextFrom("SECRET-TWO", true, sendOrigin{From: "eng2"})
	st2, _ := os.Stat(path)
	if os.SameFile(st1, st2) {
		t.Fatal("the second write rewrote the file in place; it must be a new file renamed over it")
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".initech", ".held-*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	restartHeldWindow(t, root, func(p *Pane) { parkForTest(p) })
	if s := logs.String(); strings.Contains(s, "SECRET-") {
		t.Fatalf("a held message's text reached the log:\n%s", s)
	}
}

// Feed it nothing: no file, an empty file, zero entries, panes present but
// none held: a clean start, no line, nothing queued.
func TestHeldFile_NothingHeldStartsCleanly(t *testing.T) {
	for name, body := range map[string]string{"no file": "", "empty file": "\x00", "zero entries": "version: 1\nheld: []\n"} {
		t.Run(name, func(t *testing.T) {
			lines := recordInitechLines(t)
			root := t.TempDir()
			path := filepath.Join(root, ".initech", heldFileWindowOne)
			if body != "" {
				os.MkdirAll(filepath.Dir(path), 0o700)
				if body == "\x00" {
					body = ""
				}
				os.WriteFile(path, []byte(body), 0o600)
			}
			eng2, _, _ := dndPane(t, "eng2")
			w := restartHeldWindow(t, root, nil, eng2)
			if n := w.p.QueueLen(); n != 0 {
				t.Fatalf("queued %d", n)
			}
			if got := lines.all(); len(got) != 0 {
				t.Fatalf("lines %q, want none", got)
			}
			if aside, _ := filepath.Glob(path + ".bad-*"); len(aside) != 0 {
				t.Fatalf("a clean start set aside %v", aside)
			}
		})
	}
}

// The writer does not write before the owner has read its file back: an empty
// pane at start would otherwise remove the file it has not read.
func TestHeldFile_NoWriteBeforeTheFileIsRead(t *testing.T) {
	root := t.TempDir()
	writeHeld(t, root, heldEntry{Pane: "eng1", Text: "KEEP", Enter: true, Time: heldT0, Kind: heldKindSuspended, From: "eng2", State: heldStateHeld})
	w := startHeldWindow(t, root)
	w.p.heldChanged() // an empty pane's change before the load
	if _, err := os.Stat(filepath.Join(root, ".initech", heldFileWindowOne)); err != nil {
		t.Fatalf("the held file went before it was read: %v", err)
	}
}

// AC5: one writer per file. Exactly two processes own pane queues and each
// writes its own file: window 1 (Run) and the serve daemon (RunDaemon).
func TestHeldFile_EachFileHasOneWriter(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	sort.Strings(files)
	var writers []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || selectorPath(call.Fun) != "newHeldWriter" || len(call.Args) != 2 {
					return true
				}
				writers = append(writers, funcKey(fd)+":"+selectorPath(call.Args[1]))
				return true
			})
		}
	}
	sort.Strings(writers)
	if got := strings.Join(writers, " "); got != "Run:heldFileWindowOne RunDaemon:heldFileServe" {
		t.Fatalf("held file writers %q, want window 1's Run on held.yaml and RunDaemon on held-serve.yaml", got)
	}
	if heldFileWindowOne == heldFileServe {
		t.Fatal("the two owners share one file")
	}
}

// parkForTest makes p a parked (suspended) pane as production leaves one: not
// alive, suspended.
func parkForTest(p *Pane) {
	p.mu.Lock()
	p.suspended, p.alive = true, false
	p.mu.Unlock()
}

// Arrival order across a restart: held mail goes back AHEAD of anything that
// reached the pane first.
func TestHeldFile_HeldMailGoesBackAheadOfNewMail(t *testing.T) {
	root := t.TempDir()
	writeHeld(t, root,
		heldEntry{Pane: "eng1", Text: "OLD-ONE", Enter: true, Time: heldT0, Kind: heldKindSuspended, From: "eng2", State: heldStateHeld},
		heldEntry{Pane: "eng1", Text: "OLD-TWO", Enter: true, Time: heldT0.Add(time.Second), Kind: heldKindSuspended, From: "eng2", State: heldStateHeld})
	w := restartHeldWindow(t, root, func(p *Pane) {
		parkForTest(p)
		p.EnqueueMessageFrom("NEW", true, "eng3")
	})
	if q := strings.Join(queueTexts(w.p), ","); q != "OLD-ONE,OLD-TWO,NEW" {
		t.Fatalf("queue %s, want OLD-ONE,OLD-TWO,NEW", q)
	}
}

// The no-resend rule's half on disk: a message being typed is in the file as
// typing, the drain's (redelivering) and the resume loop's (typingNext).
func TestHeldFile_AMessageBeingTypedIsWrittenAsTyping(t *testing.T) {
	p := &Pane{name: "eng1"}
	p.beginRedelivery(QueuedMessage{Text: "DRAIN", Time: heldT0})
	p.typeInHand([]QueuedMessage{{Text: "RESUME-A", Time: heldT0}, {Text: "RESUME-B", Time: heldT0}})
	p.typingNext()
	got := map[string]string{}
	for _, e := range p.heldSnapshot() {
		got[e.Text] = e.State
	}
	want := map[string]string{"DRAIN": heldStateTyping, "RESUME-A": heldStateTyping, "RESUME-B": heldStateHeld}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s written %q, want %q (snapshot %v)", k, got[k], v, got)
		}
	}
	p.typedOne()
	p.endRedelivery()
	for _, e := range p.heldSnapshot() {
		if e.State == heldStateTyping {
			t.Errorf("%s still typing after its verdict", e.Text)
		}
	}
}

// qa2's H7 (ini-8rkg): restored do-not-disturb mail is released at the
// restart, so a dialog that then stays open gives it up ten minutes after
// the restart, not after it was sent, and not never.
func TestHeldFile_RestoredDNDMailGivesUpTenMinutesAfterTheRestart(t *testing.T) {
	p, _, _ := dndPane(t, "eng1")
	restart := heldT0.Add(3 * time.Hour) // sent long before the restart
	lines := requeueHeld([]heldEntry{{Pane: "eng1", Text: "[from eng2] held", Enter: true, Time: heldT0,
		Kind: heldKindDND, From: "eng2", State: heldStateHeld}}, func(string) *Pane { return p }, restart)
	if len(lines) != 0 || p.QueuedMessageCount() != 1 {
		t.Fatalf("restore: lines %q, queued %d; want it back, nothing said", lines, p.QueuedMessageCount())
	}
	if n := p.resolveHeldMail(restart.Add(heldMessageDeadline - time.Second)); n != 0 {
		t.Fatalf("given up %d before ten minutes from the restart", n)
	}
	if n := p.resolveHeldMail(restart.Add(heldMessageDeadline)); n != 1 {
		t.Fatalf("given up %d at ten minutes from the restart, want 1", n)
	}
}
