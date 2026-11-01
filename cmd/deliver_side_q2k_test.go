package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/lifecycle"
	"github.com/nmelo/initech/internal/tui"
)

// ini-q2k: blocked, deferred and every status.custom entry after qa_passed
// (today ready_to_ship and waiting_on_nelson) are side states, not steps: a
// deliver on a bead in one is refused before any bd write. The chain ends at
// qa_passed; super closes with bd close.

const q2kCustom = "ready_for_qa,in_qa,qa_passed,ready_to_ship,waiting_on_nelson"

type q2kRun struct {
	updates, comments int
	written           []string
	sends             []tui.IPCRequest
	stderr            string
	err               error
}

// q2kDeliver runs one initech deliver of a bead at state, with status.custom
// set to custom and the bead's newest comment first line set to latest.
func q2kDeliver(t *testing.T, custom, agent, state, latest string, args ...string) q2kRun {
	t.Helper()
	stubBdFns(t)
	resetDeliverFlags(t)
	var r q2kRun
	lifecycle.ConfigGetFn = func(string) (string, error) { return custom, nil }
	bdShowBeadFn = func(id string) (string, string, string, error) { return "Side state", agent, state, nil }
	bdUpdateBeadFn = func(id string, w beadWrite) error { r.updates++; r.written = append(r.written, w.Status); return nil }
	bdCommentAddFn = func(id, author, comment string) error { r.comments++; return nil }
	old := bdLatestCommentFn
	bdLatestCommentFn = func(string) (string, error) { return latest, nil }
	t.Cleanup(func() { bdLatestCommentFn = old })
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true})
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", agent)
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs(append([]string{"deliver", "ini-q2k"}, args...))
	defer rootCmd.SetArgs(nil)
	r.err = rootCmd.Execute()
	r.stderr = errBuf.String()
	for _, q := range *received {
		if q.Action == "send" {
			r.sends = append(r.sends, q)
		}
	}
	return r
}

// AC1: a bead in a side state is refused, with its state and what to do,
// before any bd write and with no report; --fail too.
func TestDeliver_ASideStateIsRefusedBeforeAnyWrite(t *testing.T) {
	skipWindows(t)
	for _, c := range []struct{ state, words string }{
		{"waiting_on_nelson", "not delivered: ini-q2k waits on Nelson (Ship it or hold?); super moves it when he answers"},
		{"blocked", "not delivered: ini-q2k is blocked (Ship it or hold?); move it back to open or in_progress first"},
		{"deferred", "not delivered: ini-q2k is deferred (Ship it or hold?); move it back to open or in_progress first"},
		{"ready_to_ship", "not delivered: ini-q2k is ready_to_ship (Ship it or hold?); move it back to open or in_progress first"},
	} {
		for _, args := range [][]string{nil, {"--fail", "--reason", "x"}, {"-m", "DONE: x", "--to", "qa1"}} {
			t.Run(c.state+" "+strings.Join(args, " "), func(t *testing.T) {
				r := q2kDeliver(t, q2kCustom, "eng7", c.state, "Ship it or hold?", args...)
				if r.err == nil || r.err.Error() != c.words {
					t.Errorf("err = %v, want %q", r.err, c.words)
				}
				if v69Exit(r.err) != 1 {
					t.Errorf("exit %d, want 1", v69Exit(r.err))
				}
				if r.updates != 0 || r.comments != 0 {
					t.Errorf("bd writes: %d status (%v), %d comments; want none", r.updates, r.written, r.comments)
				}
				if len(r.sends) != 0 {
					t.Errorf("reports sent: %+v, want none", r.sends)
				}
			})
		}
	}
}

// AC4: no latest comment: the words without the parenthesis.
func TestDeliver_ASideStateWithNoCommentHasNoParenthesis(t *testing.T) {
	skipWindows(t)
	r := q2kDeliver(t, q2kCustom, "eng7", "blocked", "")
	if want := "not delivered: ini-q2k is blocked; move it back to open or in_progress first"; r.err == nil || r.err.Error() != want {
		t.Errorf("err = %v, want %q", r.err, want)
	}
}

// AC1: a status added after qa_passed is a side state by default.
func TestDeliver_ANewStatusAfterQAPassedIsASideState(t *testing.T) {
	skipWindows(t)
	r := q2kDeliver(t, q2kCustom+",on_hold_for_legal", "eng7", "on_hold_for_legal", "")
	if r.err == nil || !strings.Contains(r.err.Error(), "is on_hold_for_legal") || r.updates != 0 {
		t.Errorf("err = %v, %d writes; want refused, none written", r.err, r.updates)
	}
}

// AC2: the chain ends at qa_passed. QA PASS and an engineer's deliver from
// qa_passed are the terminal no-op; ready_to_ship is never written.
func TestDeliver_TheChainEndsAtQAPassed(t *testing.T) {
	skipWindows(t)
	for _, c := range []struct {
		agent string
		args  []string
	}{{"qa1", []string{"--verdict", "PASS"}}, {"eng7", nil}} {
		r := q2kDeliver(t, q2kCustom, c.agent, "qa_passed", "", c.args...)
		if r.err != nil || r.updates != 0 || len(r.sends) != 0 {
			t.Errorf("%s from qa_passed: err %v, writes %v, reports %d; want the no-op", c.agent, r.err, r.written, len(r.sends))
		}
		if !strings.Contains(r.stderr, "no-op") {
			t.Errorf("%s: stderr %q, want the no-op warning", c.agent, r.stderr)
		}
	}
}

// AC2: LoadChain stops at qa_passed with today's status.custom and with a new
// status after it.
func TestLoadChain_EndsAtQAPassed(t *testing.T) {
	orig := lifecycle.ConfigGetFn
	t.Cleanup(func() { lifecycle.ConfigGetFn = orig })
	for _, custom := range []string{q2kCustom, q2kCustom + ",on_hold_for_legal"} {
		lifecycle.ConfigGetFn = func(string) (string, error) { return custom, nil }
		chain, err := lifecycle.LoadChain()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(chain, ","); got != "open,in_progress,ready_for_qa,in_qa,qa_passed" {
			t.Errorf("custom %q: chain %s, want it to end at qa_passed", custom, got)
		}
	}
}

// AC3: a walk inside the chain is as today.
func TestDeliver_AStepInsideTheChainIsAsToday(t *testing.T) {
	skipWindows(t)
	for _, c := range []struct {
		agent, state, want string
		args               []string
	}{
		{"eng7", "in_progress", "ready_for_qa", nil},
		{"pm", "ready_for_qa", "in_qa", nil},
		{"qa1", "in_qa", "qa_passed", []string{"--verdict", "PASS"}},
	} {
		r := q2kDeliver(t, q2kCustom, c.agent, c.state, "", c.args...)
		if r.err != nil || len(r.written) != 1 || r.written[0] != c.want {
			t.Errorf("%s from %s: err %v, wrote %v, want %s", c.agent, c.state, r.err, r.written, c.want)
		}
	}
}

// AC4: status.custom without qa_passed: the chain is the custom list as
// today, deliver says so once, and only blocked and deferred are refused.
func TestDeliver_ACustomListWithoutQAPassedWalksAsToday(t *testing.T) {
	skipWindows(t)
	r := q2kDeliver(t, "design_review,code_review", "eng7", "design_review", "")
	if r.err != nil || len(r.written) != 1 || r.written[0] != "code_review" {
		t.Errorf("err %v, wrote %v; want code_review", r.err, r.written)
	}
	if n := strings.Count(r.stderr, "has no qa_passed"); n != 1 {
		t.Errorf("stderr %q: the no-qa_passed line %d times, want once", r.stderr, n)
	}
	r = q2kDeliver(t, "design_review,code_review", "eng7", "blocked", "")
	if r.err == nil || r.updates != 0 {
		t.Errorf("blocked under a custom list without qa_passed: err %v, %d writes; want refused", r.err, r.updates)
	}
}
