package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPostValidation_BodiesAndEnvelope(t *testing.T) {
	for _, tc := range []struct {
		body, def string
		valid     bool
	}{
		{"", "", false}, {" \r\n\t", "", false}, {"hi\nthere", "do it", true},
		{strings.Repeat("x", MaxInboxBodyBytes), "", true}, {strings.Repeat("x", MaxInboxBodyBytes+1), "", false},
		{"hi", strings.Repeat("x", MaxInboxDefaultBytes+1), false}, {string([]byte{255}), "", false},
	} {
		if err := ValidateInboxPost(tc.body, tc.def); (err == nil) != tc.valid {
			t.Errorf("valid=%v, err=%v", tc.valid, err)
		}
	}
	// Worst-case escaping of supported body + default stays in a single frame.
	b, err := json.Marshal(IPCRequest{Action: "post", Text: strings.Repeat("\x00", MaxInboxBodyBytes), DefaultText: strings.Repeat("\x00", MaxInboxDefaultBytes)})
	if err != nil || len(b) >= IPCScanBufSize {
		t.Fatalf("post envelope: %d, %v", len(b), err)
	}
}

func TestPostRequest_RejectsSuppliedIdentity(t *testing.T) {
	for _, raw := range []string{
		`{"action":"post","text":"hi","agent":"super"}`,
		`{"action":"post","text":"hi","name":"super"}`,
		`{"action":"post","text":"hi","as":"super"}`,
		`{"action":"post","text":"hi","run_key":"fake"}`,
		`{"action":"post","text":"hi","target":"super"}`,
		`{"action":"post","text":"hi","host":"remote"}`,
	} {
		var req IPCRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		if err := validatePostRequest(req, []byte(raw)); err == nil || !errors.Is(err, errPostSuppliedIdentity) {
			t.Errorf("accepted forged identity: %s, %v", raw, err)
		}
	}
	req := IPCRequest{Action: "post", Text: "hi"}
	raw, _ := json.Marshal(req)
	if err := validatePostRequest(req, raw); err != nil {
		t.Fatalf("CLI wire shape refused: %v", err)
	}
}

func TestPostIPC_AllStatesAndOwnerWithdrawal(t *testing.T) {
	app := &TUI{}
	id := postIdentity{"eng3", "process-1"}
	now := time.Now()
	post := app.applyPostRequest(IPCRequest{Action: "post", Text: "hello"}, id, now)
	if !post.OK || post.Data != "posted p1" {
		t.Fatalf("post: %+v", post)
	}
	check := func() IPCResponse {
		return app.applyPostRequest(IPCRequest{Action: "post_check", ItemID: "p1"}, id, now)
	}
	if got := check(); got.Data != inboxUnreadStatus {
		t.Fatalf("unread: %+v", got)
	}
	if err := app.inboxState().Transition("p1", InboxSeen, actorOperator); err != nil {
		t.Fatal(err)
	}
	if got := check(); !strings.HasPrefix(got.Data, "seen ") {
		t.Fatalf("seen: %+v", got)
	}
	if err := app.inboxState().Answer("p1", "the answer\nwith a second line"); err != nil {
		t.Fatal(err)
	}
	if got := check(); !strings.HasPrefix(got.Data, "answered: the answer\nwith a second line") {
		t.Fatalf("answer: %+v", got)
	}
	refused := app.applyPostRequest(IPCRequest{Action: "post_withdraw", ItemID: "p1"}, id, now)
	if refused.OK || !strings.Contains(refused.Error, "already answered: the answer") {
		t.Fatalf("terminal withdrawal: %+v", refused)
	}
	for _, mode := range []string{"post_check", "post_withdraw"} {
		other := app.applyPostRequest(IPCRequest{Action: mode, ItemID: "p1"}, postIdentity{"eng2", "other"}, now)
		if other.OK || !strings.Contains(other.Error, "another agent") {
			t.Fatalf("owner check: %+v", other)
		}
		missing := app.applyPostRequest(IPCRequest{Action: mode, ItemID: "p99"}, id, now)
		if missing.OK || missing.Error != "no item p99" {
			t.Fatalf("missing: %+v", missing)
		}
	}
	app.applyPostRequest(IPCRequest{Action: "post", Text: "resolved myself"}, id, now)
	withdrawn := app.applyPostRequest(IPCRequest{Action: "post_withdraw", ItemID: "p2"}, id, now)
	if !withdrawn.OK || withdrawn.Data != "withdrawn p2" {
		t.Fatalf("withdraw: %+v", withdrawn)
	}
	got := app.applyPostRequest(IPCRequest{Action: "post_check", ItemID: "p2"}, id, now)
	if got.Data != "withdrawn" {
		t.Fatalf("withdrawn check: %+v", got)
	}
	app.applyPostRequest(IPCRequest{Action: "post", Text: "dismiss this"}, id, now)
	if err := app.inboxState().Transition("p3", InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	got = app.applyPostRequest(IPCRequest{Action: "post_check", ItemID: "p3"}, id, now)
	if got.Data != "dismissed" {
		t.Fatalf("dismissed check: %+v", got)
	}
	mine := app.applyPostRequest(IPCRequest{Action: "post_mine"}, postIdentity{"eng3", "process-2"}, now)
	for _, status := range []string{"p1 answered", "p2 withdrawn", "p3 dismissed"} {
		if !strings.Contains(mine.Data, status) {
			t.Errorf("restart loses history: %+v", mine)
		}
	}
}

func TestPostIPC_TeachingsAggregateAndResetWithPaneProcess(t *testing.T) {
	app := &TUI{}
	now := time.Now()
	first := postIdentity{"eng3", postProcessRunKey(42, now)}
	restarted := postIdentity{"eng3", postProcessRunKey(43, now.Add(time.Minute))}
	// This initial question declares its default, avoiding B's tip, and is dismissed.
	app.applyPostRequest(IPCRequest{Action: "post", Text: "update docs?", DefaultText: "update both"}, first, now)
	if err := app.inboxState().Transition("p1", InboxDismissed, actorOperator); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		app.applyPostRequest(IPCRequest{Action: "post", Text: fmt.Sprintf("heads up %d", n)}, first, now)
	}
	got := app.applyPostRequest(IPCRequest{Action: "post", Text: "update docs?"}, first, now)
	joined := strings.Join(got.Notices, "\n")
	for _, condition := range []string{"Dismissed means no", "you now have 6 items waiting", "--default"} {
		if !strings.Contains(joined, condition) {
			t.Errorf("lost simultaneous %s teaching: %+v", condition, got)
		}
	}
	if len(got.Notices) < 2 {
		t.Fatalf("store notices overwrote CLI teaching: %+v", got)
	}
	next := app.applyPostRequest(IPCRequest{Action: "post", Text: "update docs?"}, first, now)
	if len(next.Notices) != 0 {
		t.Fatalf("same run retaught: %+v", next)
	}
	next = app.applyPostRequest(IPCRequest{Action: "post", Text: "update docs?"}, restarted, now)
	joined = strings.Join(next.Notices, "\n")
	if !strings.Contains(joined, "dismiss") || !strings.Contains(joined, "--default") {
		t.Fatalf("process restart not supplied to both latches: %+v", next)
	}
}

func TestPostTeaching_PollingWindowAndPerRunLatch(t *testing.T) {
	var state postTeachingState
	id := postIdentity{"eng3", "run1"}
	now := time.Unix(10000, 0)
	if state.check(id, "p1", now) != "" || state.check(id, "p1", now.Add(time.Minute)) != "" {
		t.Fatal("taught before third check")
	}
	got := state.check(id, "p1", now.Add(4*time.Minute))
	if !strings.Contains(got, "checked 3 times in 4m0s") || !strings.Contains(got, "stop polling") {
		t.Fatalf("poll teaching: %q", got)
	}
	for n := 0; n < 3; n++ {
		if state.check(id, "p2", now) != "" {
			t.Fatal("retaught on new item in same run")
		}
	}
	id.runKey = "run2"
	for n := 0; n < 2; n++ {
		if state.check(id, "p1", now.Add(time.Duration(n)*time.Minute)) != "" {
			t.Fatal("restart counter inherited")
		}
	}
	if state.check(id, "p1", now.Add(2*time.Minute)) == "" {
		t.Fatal("restart did not reteach")
	}
	id.runKey = "run3"
	state.check(id, "p1", now)
	state.check(id, "p1", now.Add(6*time.Minute))
	if state.check(id, "p1", now.Add(7*time.Minute)) != "" {
		t.Fatal("expired check counted")
	}
	if state.check(id, "p1", now.Add(8*time.Minute)) == "" {
		t.Fatal("new window not counted")
	}
}

func TestPostMine_BoundsAggregateHistoryWithoutHidingOmission(t *testing.T) {
	var items []InboxItem
	for i := 0; i < 75; i++ {
		items = append(items, InboxItem{ID: fmt.Sprintf("p%d", i+1), Agent: "eng3", Body: strings.Repeat("<", MaxInboxBodyBytes), ReplyText: strings.Repeat("answer", MaxInboxBodyBytes), State: InboxAnswered, Created: time.Unix(int64(i), 0)})
	}
	data := formatInboxMine(items)
	lines := strings.Split(data, "\n")
	if len(lines) != 51 || lines[0] != "p75 answered" || lines[49] != "p26 answered" || lines[50] != "… and 25 more — initech post --check <id>" {
		t.Fatalf("bounded listing: %s", data)
	}
	wire, _ := json.Marshal(IPCResponse{OK: true, Data: data})
	if len(wire) >= IPCScanBufSize {
		t.Fatalf("listing exceeds envelope: %d", len(wire))
	}
}

func TestPostIPC_EmptyAndFollowerRefusedWithoutRecording(t *testing.T) {
	for _, app := range []*TUI{{}, {windowID: "window2"}} {
		resp := app.applyPostRequest(IPCRequest{Action: "post", Text: " "}, postIdentity{"eng3", "run"}, time.Now())
		if resp.OK || !strings.Contains(resp.Error, "usage:") {
			t.Fatalf("empty: %+v", resp)
		}
		if len(app.inboxState().Items()) != 0 {
			t.Fatal("empty recorded")
		}
	}
	follower := &TUI{windowID: "window2"}
	resp := follower.applyPostRequest(IPCRequest{Action: "post", Text: "hello"}, postIdentity{"eng3", "run"}, time.Now())
	if resp.OK || len(follower.inboxState().Items()) != 0 {
		t.Fatalf("follower wrote: %+v", resp)
	}
}

func TestPostDaemon_RefusesAllInboxActions(t *testing.T) {
	for _, action := range []string{"post", "post_check", "post_mine", "post_withdraw"} {
		resp := postTestResponse(t, func(c net.Conn) {
			if !(&Daemon{}).HandleExtended(c, IPCRequest{Action: action}, nil) {
				t.Error("action fell through")
			}
		})
		if resp.OK || !strings.Contains(resp.Error, "remote daemon") || !strings.Contains(resp.Error, "operator's local fleet") {
			t.Fatalf("daemon refusal: %+v", resp)
		}
	}
}

func TestPostIPC_CheckReportsDeliverySeparatelyFromAnswer(t *testing.T) {
	for _, status := range []string{"", "delivered", "held — agent has a dialog open; delivers when it clears", "typed, awaiting submit", "queued — agent suspended; wakes and delivers", "not running — stored"} {
		t.Run(status, func(t *testing.T) {
			app := &TUI{}
			identity := postIdentity{"eng3", "run"}
			result, err := app.inboxState().Post(InboxItem{Agent: identity.agent, Body: "question"}, identity.runKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := app.inboxState().Answer(result.ID, "yes"); err != nil {
				t.Fatal(err)
			}
			// D owns the callback; use A's public write to supply its outcome.
			if err := app.inboxState().SetDeliveryStatus(result.ID, status); err != nil {
				t.Fatal(err)
			}
			response := app.applyPostRequest(IPCRequest{Action: "post_check", ItemID: result.ID}, identity, time.Now())
			expected := status
			if expected == "" {
				expected = "unconfirmed"
			}
			if !response.OK || response.Data != "answered: yes — delivery: "+expected {
				t.Fatalf("delivery status: %+v", response)
			}
		})
	}
}
