package tui

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ini-r0q.14: Claude Code's folder trust prompt highlights "No, exit", so a
// blind Enter ends the agent; Esc does too, and a second Ctrl-C. Those keys
// are refused there, words saying why, whoever sends them; y and n do nothing
// there and go in. Everything is read from captured screens.

const (
	r0q14EnterOnNo  = "the folder trust prompt is up and Enter would choose No, exit; open the pane to answer it"
	r0q14EnterOnYes = "the folder trust prompt is up and Enter would choose Yes, I trust this folder; open the pane to answer it"
	r0q14Esc        = "the folder trust prompt is up and Esc would exit Claude; open the pane to answer it"
	r0q14CtrlC      = "the folder trust prompt is up and Ctrl-C twice would exit Claude; open the pane to answer it"
)

// r0q14Shapes is every captured trust prompt: each version and width, the
// option wrapped under 25 columns, the cursor moved to Yes, and the footer
// while Ctrl-C is armed (no "Enter to confirm" on it).
var r0q14Shapes = []struct {
	file  string
	onYes bool
}{
	{"haiku_trust_prompt.txt", false},
	{"haiku310_trust_prompt.txt", false},
	{"haiku312_trust_w32.txt", false},
	{"haiku312_trust_w80.txt", false},
	{"haiku316_trust_w100.txt", false},
	{"haiku316_trust_w40.txt", false},
	{"haiku316_trust_w29.txt", false},
	{"haiku316_trust_w24.txt", false},
	{"haiku316_trust_w20.txt", false},
	{"haiku316_trust_cursor_yes_w100.txt", true},
	{"haiku316_trust_armed_w100.txt", false},
	{"haiku316_trust_armed_w32.txt", false},
}

// r0q14Paint puts a capture on p's screen at the capture's own width.
func r0q14Paint(t *testing.T, p *Pane, rows []string) {
	t.Helper()
	width := 10
	for _, r := range rows {
		width = max(width, utf8.RuneCountInString(r))
	}
	p.emu.Resize(width, len(rows)+2)
	lvxwPaint(p, rows)
}

// r0q14At is a live pane showing the capture, and what reaches its PTY.
func r0q14At(t *testing.T, file string) (*Pane, func() string) {
	t.Helper()
	shortSubmitDelays(t)
	p, _, typed := dndPane(t, "a")
	r0q14Paint(t, p, lvxwFixture(t, file))
	return p, typed
}

// r0q14Refused asserts a key is not delivered with exactly these words and
// that nothing reached the PTY.
func r0q14Refused(t *testing.T, p *Pane, typed func() string, key string, o sendOrigin, words string) {
	t.Helper()
	v := keystrokePane(p, key, o)
	if v.Outcome != DeliveryNotDelivered || v.Reason != words {
		t.Errorf("%s from %q: %q (%s), want not delivered: %s", key, o.From, v.Outcome, v.Reason, words)
	}
	if got := r0q9Typed(typed, ""); got != "" {
		t.Errorf("%s from %q: the PTY got %q, want nothing", key, o.From, got)
	}
}

// r0q14Delivered asserts a key goes in, its bytes and nothing else.
func r0q14Delivered(t *testing.T, p *Pane, typed func() string, key string, o sendOrigin) {
	t.Helper()
	v := keystrokePane(p, key, o)
	if v.Outcome != DeliveryDelivered {
		t.Errorf("%s from %q: %q (%s), want delivered", key, o.From, v.Outcome, v.Reason)
	}
	if got := r0q9Typed(typed, r0q9Bytes[key]); got != r0q9Bytes[key] {
		t.Errorf("%s from %q: the PTY got %q, want %q", key, o.From, got, r0q9Bytes[key])
	}
}

var r0q14Operator = sendOrigin{From: operatorSender}

// AC1: Enter from the operator is refused on every shape, the words naming
// the row the cursor is on; nothing reaches the PTY.
func TestTrustKey_EnterIsRefusedOnEveryShape(t *testing.T) {
	for _, s := range r0q14Shapes {
		t.Run(s.file, func(t *testing.T) {
			p, typed := r0q14At(t, s.file)
			want := r0q14EnterOnNo
			if s.onYes {
				want = r0q14EnterOnYes
			}
			r0q14Refused(t, p, typed, "enter", r0q14Operator, want)
			if !p.IsAlive() {
				t.Error("the refusal touched the pane")
			}
		})
	}
}

// AC2: Esc ends the process whichever row the cursor is on.
func TestTrustKey_EscIsRefusedOnEveryShape(t *testing.T) {
	for _, s := range r0q14Shapes {
		t.Run(s.file, func(t *testing.T) {
			p, typed := r0q14At(t, s.file)
			r0q14Refused(t, p, typed, "esc", r0q14Operator, r0q14Esc)
		})
	}
}

// AC2: a second Ctrl-C ends the process, and two taps can beat a repaint, so
// Ctrl-C is refused outright, armed or not.
func TestTrustKey_CtrlCIsRefusedOnEveryShape(t *testing.T) {
	for _, s := range r0q14Shapes {
		t.Run(s.file, func(t *testing.T) {
			p, typed := r0q14At(t, s.file)
			r0q14Refused(t, p, typed, "ctrl-c", r0q14Operator, r0q14CtrlC)
		})
	}
}

// AC2: y and n do nothing at the trust prompt and stay delivered.
func TestTrustKey_YAndNAreDeliveredOnEveryShape(t *testing.T) {
	for _, s := range r0q14Shapes {
		for _, key := range []string{"y", "n"} {
			t.Run(s.file+"/"+key, func(t *testing.T) {
				p, typed := r0q14At(t, s.file)
				r0q14Delivered(t, p, typed, key, r0q14Operator)
			})
		}
	}
}

// AC1: the same words reach the socket's reply (initech keystroke and POST
// /v1/keys read this reply) and a peer's control command.
func TestTrustKey_TheRepliesCarryTheWords(t *testing.T) {
	for _, s := range r0q14Shapes {
		t.Run(s.file, func(t *testing.T) {
			p, typed := r0q14At(t, s.file)
			want := r0q14EnterOnNo
			if s.onYes {
				want = r0q14EnterOnYes
			}
			r := dndCall(t, func(c net.Conn) {
				dispatchIPC(newTestTUI(p), c, IPCRequest{Action: "keystroke", Target: "a", Text: "enter", From: operatorSender}, nil)
			})
			if r.OK || r.Delivery != DeliveryNotDelivered || r.Error != want {
				t.Errorf("IPC reply %+v, want not delivered: %s", r, want)
			}
			d := &Daemon{panes: []*Pane{p}}
			cr := d.keystrokeControl(ControlCmd{Action: "keystroke", Target: "a", Text: "enter", From: operatorSender})
			if cr.OK || cr.Delivery != DeliveryNotDelivered || cr.Error != want {
				t.Errorf("control reply %+v, want not delivered: %s", cr, want)
			}
			if got := r0q9Typed(typed, ""); got != "" {
				t.Errorf("the PTY got %q", got)
			}
		})
	}
}

// AC3: another agent's key is refused with the dialog words on every shape,
// the armed one (whose footer the dialog guard cannot read) included.
func TestTrustKey_AnAgentsKeyGetsTheDialogWords(t *testing.T) {
	for _, s := range r0q14Shapes {
		t.Run(s.file, func(t *testing.T) {
			for _, o := range []sendOrigin{{From: "eng1"}, {From: "super", Urgent: true}, {From: ""}} {
				for _, key := range KeystrokeKeyNames {
					p, typed := r0q14At(t, s.file)
					r0q14Refused(t, p, typed, key, o, keystrokeDialogReason)
				}
			}
		})
	}
}

// AC3: at a numbered permission prompt Enter is still Yes and Esc still No.
func TestTrustKey_TheNumberedPromptKeepsItsKeys(t *testing.T) {
	for _, file := range []string{
		"haiku312_rm_guard_prompt_w32.txt",
		"haiku312_rm_guard_prompt_w80.txt",
		"haiku310_write_prompt.txt",
		"haiku310_bash_prompt_bgagent.txt",
		"haiku_write_prompt.txt",
		"haiku_edit_prompt.txt",
	} {
		for _, key := range KeystrokeKeyNames {
			t.Run(file+"/"+key, func(t *testing.T) {
				p, typed := r0q14At(t, file)
				if !paneShowsModalOnScreen(p) {
					t.Fatal("precondition: the prompt reads as a dialog")
				}
				r0q14Delivered(t, p, typed, key, r0q14Operator)
			})
		}
	}
}

// AC3: a resting prompt and every other dialog take every key as before.
func TestTrustKey_AnyOtherScreenTakesEveryKey(t *testing.T) {
	for _, file := range []string{
		"haiku310_footer_idle.txt",
		"haiku_idle.txt",
		"haiku_busy_spinner.txt",
		"haiku310_picker.txt",
		"haiku310_login.txt",
	} {
		for _, key := range KeystrokeKeyNames {
			t.Run(file+"/"+key, func(t *testing.T) {
				p, typed := r0q14At(t, file)
				r0q14Delivered(t, p, typed, key, r0q14Operator)
			})
		}
	}
}

// A resting agent's transcript may quote the prompt (this very bead does):
// above a resting input box it is not the prompt, and Enter goes in.
func TestTrustKey_APromptQuotedAboveARestingBoxIsNotThePrompt(t *testing.T) {
	rule := strings.Repeat("─", 80)
	rows := append(lvxwFixture(t, "haiku312_trust_w80.txt"), "", "✻ Worked for 3s", "", rule, "❯ ", rule, "  ⏵⏵ bypass permissions on")
	p, _, typed := dndPane(t, "a")
	r0q14Paint(t, p, rows)
	r0q14Delivered(t, p, typed, "enter", r0q14Operator)
}

// With no input box on screen every row is read, so the words must be the two
// option rows, one directly under the other: the phrases apart in a paragraph
// are not the prompt.
func TestTrustKey_ThePhrasesApartAreNotThePrompt(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	r0q14Paint(t, p, []string{
		"The prompt highlights No, exit by default, so Enter ends the agent.",
		"The other option reads Yes, I trust this folder and takes a Down first.",
	})
	r0q14Delivered(t, p, typed, "enter", r0q14Operator)
}

// The banner kind reads the same definition: every trust capture is
// trust_prompt, wrapped and armed ones too, and no other capture in the corpus
// is. A false match here would refuse a key on a screen that is not the prompt.
func TestTrustKey_EveryCaptureInTheCorpusIsReadRight(t *testing.T) {
	var files []string
	for _, pattern := range []string{"*.txt", filepath.Join("footer_corpus", "*.txt")} {
		found, err := filepath.Glob(filepath.Join("testdata", pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, found...)
	}
	if len(files) < 60 {
		t.Fatalf("read only %d captures: the census is not over the corpus", len(files))
	}
	trust := 0
	for _, f := range files {
		name, err := filepath.Rel("testdata", f)
		if err != nil {
			t.Fatal(err)
		}
		isTrust := strings.Contains(name, "_trust")
		if isTrust {
			trust++
		}
		if got := bannerKind(lvxwFixture(t, name), false) == bannerTrust; got != isTrust {
			t.Errorf("%s: read as the trust prompt = %v, want %v", name, got, isTrust)
		}
	}
	if trust != len(r0q14Shapes) {
		t.Errorf("%d trust captures in the corpus, %d in the shape list: a shape is untested", trust, len(r0q14Shapes))
	}
}

// The footer is not read: the option rows alone, with no input box under them,
// are the prompt (the near-miss twin is in TestBanner_NearMissesAreNone).
func TestTrustKey_TheOptionRowsAloneAreThePrompt(t *testing.T) {
	if got := bannerKind([]string{" ❯ No, exit", "   Yes, I trust this folder"}, false); got != bannerTrust {
		t.Errorf("banner %q, want %q", got, bannerTrust)
	}
}

// AC4: a suspended pane's verdict comes first, the prompt on its screen or not.
func TestTrustKey_ASuspendedPaneKeepsItsVerdict(t *testing.T) {
	p, typed := r0q14At(t, "haiku312_trust_w80.txt")
	p.SetSuspended(true)
	r0q14Refused(t, p, typed, "enter", r0q14Operator, "a is suspended; a key does not wake it")
}

// AC4: do-not-disturb's verdict comes before the prompt's.
func TestTrustKey_DoNotDisturbKeepsItsVerdict(t *testing.T) {
	p, typed := r0q14At(t, "haiku312_trust_w80.txt")
	p.startDND()
	v := keystrokePane(p, "enter", sendOrigin{From: "eng1"})
	if v.Outcome != DeliveryNotDelivered || !strings.HasSuffix(v.Reason, "; a key is never queued") {
		t.Errorf("%q (%s), want do-not-disturb's refusal", v.Outcome, v.Reason)
	}
	if got := r0q9Typed(typed, ""); got != "" {
		t.Errorf("the PTY got %q", got)
	}
}

// AC4: a pane with no PTY is not running, whatever its last screen shows.
func TestTrustKey_APaneNotRunningKeepsItsVerdict(t *testing.T) {
	p, typed := r0q14At(t, "haiku312_trust_w80.txt")
	p.ptmx = nil
	r0q14Refused(t, p, typed, "enter", r0q14Operator, "a is not running")
}

// AC4: a dead pane keeps showing the prompt it died at (the repro's last
// screen). Its verdict stays the write's failure: never the trust words, which
// would send the operator to answer a prompt nothing is listening to, and
// never delivered.
func TestTrustKey_ADeadPaneKeepsItsVerdict(t *testing.T) {
	p, _ := r0q14At(t, "haiku312_trust_w80.txt")
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
	p.ptmx.Close()
	v := keystrokePane(p, "enter", r0q14Operator)
	if v.Outcome != DeliveryNotDelivered || !strings.HasPrefix(v.Reason, "a: ") {
		t.Errorf("%q (%s), want not delivered with the write's error", v.Outcome, v.Reason)
	}
}

// AC4: a screen that cannot be read takes the existing path, inside its
// bound: the key never waits on a wedged emulator, and this is the stated
// residue (delivered, the prompt unseen).
func TestTrustKey_AHeldScreenTakesTheExistingPath(t *testing.T) {
	p, typed := r0q14At(t, "haiku312_trust_w80.txt")
	p.renderMu.Lock()
	done := make(chan SendVerdict, 1)
	go func() { done <- keystrokePane(p, "enter", r0q14Operator) }()
	select {
	case v := <-done:
		p.renderMu.Unlock()
		if v.Outcome != DeliveryDelivered {
			t.Errorf("%q (%s), want the existing verdict: delivered", v.Outcome, v.Reason)
		}
		if got := r0q9Typed(typed, "\r"); got != "\r" {
			t.Errorf("the PTY got %q", got)
		}
	case <-time.After(5 * time.Second):
		p.renderMu.Unlock()
		t.Fatal("the key waited on a held screen")
	}
	p.mu.Lock()
	skipped := !p.screenSkipSince.IsZero()
	p.mu.Unlock()
	if !skipped {
		t.Error("the skip was not recorded: a wedged pane reads as unmaintained silently")
	}
}

// A lock held only for a moment (a render) does not cost the guard its read:
// the budget outlasts it, and the key is refused.
func TestTrustKey_AMomentaryHoldStillReadsThePrompt(t *testing.T) {
	old := keystrokeScreenBudget
	keystrokeScreenBudget = 10 * time.Second
	t.Cleanup(func() { keystrokeScreenBudget = old })
	p, typed := r0q14At(t, "haiku312_trust_w80.txt")
	p.renderMu.Lock()
	done := make(chan SendVerdict, 1)
	go func() { done <- keystrokePane(p, "enter", r0q14Operator) }()
	time.Sleep(20 * time.Millisecond) // past one 2 ms try
	p.renderMu.Unlock()
	if v := <-done; v.Outcome != DeliveryNotDelivered || v.Reason != r0q14EnterOnNo {
		t.Errorf("%q (%s), want the refusal", v.Outcome, v.Reason)
	}
	if got := r0q9Typed(typed, ""); got != "" {
		t.Errorf("the PTY got %q", got)
	}
}

// A blank screen (a pane just started, or feeding the guard nothing) takes
// every key.
func TestTrustKey_ABlankScreenTakesTheKey(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	r0q14Delivered(t, p, typed, "enter", r0q14Operator)
}
