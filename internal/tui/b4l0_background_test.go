package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ini-b4l0: an agent whose footer shows background work is recorded as busy
// with it, from the once-a-second tick's own screen read, and the list and
// patrol replies carry it so status and patrol can say so.

func b4l0Footer(t *testing.T, file string) Footer {
	t.Helper()
	return parseFooter(lvxwFixture(t, file), 160)
}

func b4l0Want(t *testing.T, step string, p *Pane, busy bool, shells, monitors, tasks int, since time.Time) {
	t.Helper()
	w, ok := p.backgroundWorkNow()
	if ok != busy {
		t.Fatalf("%s: busy %v, want %v (%+v)", step, ok, busy, w)
	}
	if !busy {
		return
	}
	if w.shells != shells || w.monitors != monitors || w.tasks != tasks || !w.since.Equal(since) {
		t.Errorf("%s: %+v, want %d shells %d monitors %d tasks since %v", step, w, shells, monitors, tasks, since)
	}
}

// Recorded from the captured footers; first-seen time kept while work goes
// on; an unread footer changes nothing; the no-background shape clears it;
// so does the process ending.
func TestBackground_TheFooterRecordsAndClearsWork(t *testing.T) {
	p := newEmuPane("eng1", 160, 45)
	t0 := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)

	p.observeBackground(t0, true, b4l0Footer(t, "haiku310_footer_idle.txt"))
	b4l0Want(t, "idle", p, false, 0, 0, 0, time.Time{})

	t1 := t0.Add(time.Second)
	p.observeBackground(t1, true, b4l0Footer(t, "haiku310_footer_shell.txt"))
	b4l0Want(t, "a shell", p, true, 1, 0, 0, t1)

	p.observeBackground(t1.Add(time.Minute), true, b4l0Footer(t, "haiku310_footer_shell_monitor.txt"))
	b4l0Want(t, "and a monitor", p, true, 1, 1, 0, t1)

	p.observeBackground(t1.Add(2*time.Minute), true, b4l0Footer(t, "haiku310_footer_bgagent.txt"))
	b4l0Want(t, "and an agent", p, true, 1, 1, 1, t1)

	// A dialog over the footer: nothing read, nothing changed.
	p.observeBackground(t1.Add(3*time.Minute), true, b4l0Footer(t, "haiku310_write_prompt.txt"))
	b4l0Want(t, "footer hidden", p, true, 1, 1, 1, t1)

	t4 := t1.Add(4 * time.Minute)
	p.observeBackground(t4, true, b4l0Footer(t, "haiku310_footer_idle.txt"))
	b4l0Want(t, "work ended", p, false, 0, 0, 0, time.Time{})

	t5 := t4.Add(time.Second)
	p.observeBackground(t5, true, b4l0Footer(t, "haiku310_footer_shell.txt"))
	b4l0Want(t, "a new shell", p, true, 1, 0, 0, t5)

	p.observeBackground(t5.Add(time.Second), false, b4l0Footer(t, "haiku310_footer_shell.txt"))
	b4l0Want(t, "process ended", p, false, 0, 0, 0, time.Time{})
}

// Three shells, and a cut mode line keeps the kinds it could not read.
func TestBackground_CountsAsTheFooterGivesThem(t *testing.T) {
	frame := []string{"", strings.Repeat("─", 60), "❯ ", strings.Repeat("─", 60)}
	p := newEmuPane("eng1", 60, 10)
	t0 := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	p.observeBackground(t0, true, parseFooter(append(frame, "  ⏸ manual mode on · 3 shells, 1 monitor"), 60))
	b4l0Want(t, "3 shells", p, true, 3, 1, 0, t0)
	// Cut after the shells: monitors not read, so the monitor stays.
	p.observeBackground(t0.Add(time.Second), true, parseFooter(append(frame, "  ⏸ manual mode on · 2 shells, 1 mo"), 37))
	b4l0Want(t, "cut", p, true, 2, 1, 0, t0)
}

// The tick reaches it: a pane painted with a busy footer is busy after one
// maintenance pass, with no other read of its screen.
func TestBackground_TheTickRecordsIt(t *testing.T) {
	p := newEmuPane("eng1", 160, 45)
	lvxwPaint(p, lvxwFixture(t, "haiku310_footer_shell_monitor.txt"))
	tu := &TUI{panes: toPaneViews([]*Pane{p})}
	now := time.Now()
	tu.modalMaintenance(now)
	b4l0Want(t, "after a tick", p, true, 1, 1, 0, now)
}

// The list and patrol replies carry it, only while there is work.
func TestBackground_ListAndPatrolCarryIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix domain socket directly")
	}
	busy := newEmuPane("eng1", 160, 45)
	busy.observeBackground(time.Now().Add(-90*time.Second), true, b4l0Footer(t, "haiku310_footer_shell.txt"))
	quiet := newEmuPane("eng2", 160, 45)
	tu := &TUI{panes: toPaneViews([]*Pane{busy, quiet})}

	dir, err := os.MkdirTemp("", "b4l")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "b.sock")
	cleanup, err := tu.startIPC(sock)
	if err != nil {
		t.Fatalf("startIPC: %v", err)
	}
	defer cleanup()

	for _, action := range []string{"list", "patrol"} {
		resp := ipcAsk(t, sock, IPCRequest{Action: action})
		if !resp.OK {
			t.Fatalf("%s: %s", action, resp.Error)
		}
		got := l6omByName(t, resp.Data)
		var b PaneBusy
		if err := json.Unmarshal(got["eng1"]["busy"], &b); err != nil {
			t.Fatalf("%s eng1 busy %s: %v", action, got["eng1"]["busy"], err)
		}
		if b.Shells != 1 || b.Monitors != 0 || b.Tasks != 0 || b.SinceSec < 89 || b.SinceSec > 95 {
			t.Errorf("%s eng1 busy = %+v, want 1 shell for about 90s", action, b)
		}
		if v, ok := got["eng2"]["busy"]; ok {
			t.Errorf("%s eng2 busy = %s, want it left out", action, v)
		}
	}
}
