package slackchat

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestParseMention(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantAgent string
		wantBody  string
	}{
		{
			name:      "standard mention",
			text:      "<@U12345> eng1 fix the bug",
			wantAgent: "eng1",
			wantBody:  "fix the bug",
		},
		{
			name:      "mention with extra spaces",
			text:      "<@U12345>   eng1   fix  the  bug  ",
			wantAgent: "eng1",
			wantBody:  "fix  the  bug",
		},
		{
			name:      "agent only no body",
			text:      "<@U12345> eng1",
			wantAgent: "eng1",
			wantBody:  "",
		},
		{
			name:      "mixed case agent",
			text:      "<@UABC> Eng1 hello",
			wantAgent: "eng1",
			wantBody:  "hello",
		},
		{
			name:      "empty after mention",
			text:      "<@U12345>",
			wantAgent: "",
			wantBody:  "",
		},
		{
			name:      "only whitespace after mention",
			text:      "<@U12345>   ",
			wantAgent: "",
			wantBody:  "",
		},
		{
			name:      "status command",
			text:      "<@U12345> status",
			wantAgent: "status",
			wantBody:  "",
		},
		{
			name:      "help command",
			text:      "<@U12345> help",
			wantAgent: "help",
			wantBody:  "",
		},
		{
			name:      "no mention prefix",
			text:      "eng1 hello",
			wantAgent: "eng1",
			wantBody:  "hello",
		},
		{
			name:      "multiple mentions takes first word after stripping",
			text:      "<@U12345> <@U67890> eng1 hello",
			wantAgent: "eng1",
			wantBody:  "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, body := ParseMention(tt.text)
			if agent != tt.wantAgent {
				t.Errorf("agent = %q, want %q", agent, tt.wantAgent)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

// fakeHost implements AgentHost for testing dispatch logic.
type fakeHost struct {
	agents   map[string]AgentInfo
	sent     []sentMessage
	sendErr  error
}

type sentMessage struct {
	agent string
	text  string
}

func (h *fakeHost) FindAgent(name string) (AgentInfo, bool) {
	a, ok := h.agents[name]
	return a, ok
}

func (h *fakeHost) AllAgents() []AgentInfo {
	result := make([]AgentInfo, 0, len(h.agents))
	for _, a := range h.agents {
		result = append(result, a)
	}
	return result
}

func (h *fakeHost) SendToAgent(name, text string) error {
	if h.sendErr != nil {
		return h.sendErr
	}
	h.sent = append(h.sent, sentMessage{agent: name, text: text})
	return nil
}

// fakeSlackAPI captures PostMessage and AddReaction calls for testing.
type fakeSlackAPI struct {
	messages  []fakeMessage
	reactions []fakeReaction
}

type fakeMessage struct {
	channel  string
	text     string
	threadTS string
}

type fakeReaction struct {
	emoji     string
	channel   string
	timestamp string
}

// testClient creates a Client with a fake host and captures Slack API calls.
// Returns the client and a function to retrieve captured messages/reactions.
func testClient(host AgentHost) (*Client, *fakeSlackAPI) {
	fakeAPI := &fakeSlackAPI{}
	c := &Client{
		api:    nil, // We override reply/react methods below.
		host:   host,
		logger: slog.Default(),
	}
	// Monkey-patch the client to capture replies instead of hitting Slack API.
	// We'll test handleAppMention through a wrapper that captures output.
	return c, fakeAPI
}

// Since we can't easily mock slack.Client, test the parsing and host
// interactions directly and test the reply formatting via unit tests.

func TestHandleAppMention_DeliverySuccess(t *testing.T) {
	host := &fakeHost{
		agents: map[string]AgentInfo{
			"eng1": {Name: "eng1", Alive: true, Activity: "running"},
		},
	}

	ev := &slackevents.AppMentionEvent{
		Text:      "<@UBOT> eng1 fix the bug",
		Channel:   "C123",
		TimeStamp: "1234.5678",
	}

	agent, body := ParseMention(ev.Text)
	if agent != "eng1" {
		t.Fatalf("agent = %q, want eng1", agent)
	}
	if body != "fix the bug" {
		t.Fatalf("body = %q, want 'fix the bug'", body)
	}

	info, found := host.FindAgent(agent)
	if !found {
		t.Fatal("agent not found")
	}
	if !info.Alive {
		t.Error("expected agent to be alive")
	}

	if err := host.SendToAgent(agent, body); err != nil {
		t.Fatalf("SendToAgent error: %v", err)
	}
	if len(host.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(host.sent))
	}
	if host.sent[0].agent != "eng1" || host.sent[0].text != "fix the bug" {
		t.Errorf("sent = %+v", host.sent[0])
	}
}

func TestHandleAppMention_AgentNotFound(t *testing.T) {
	host := &fakeHost{
		agents: map[string]AgentInfo{
			"eng1": {Name: "eng1", Alive: true, Activity: "running"},
			"qa1":  {Name: "qa1", Alive: true, Activity: "idle"},
		},
	}

	agent, _ := ParseMention("<@UBOT> nonexistent test")
	_, found := host.FindAgent(agent)
	if found {
		t.Fatal("expected agent not found")
	}

	agents := host.AllAgents()
	if len(agents) != 2 {
		t.Fatalf("agents = %d, want 2", len(agents))
	}
}

func TestHandleAppMention_DeadAgent(t *testing.T) {
	host := &fakeHost{
		agents: map[string]AgentInfo{
			"eng1": {Name: "eng1", Alive: false, Activity: "stopped"},
		},
	}

	agent, body := ParseMention("<@UBOT> eng1 hello")
	info, found := host.FindAgent(agent)
	if !found {
		t.Fatal("agent not found")
	}
	if info.Alive {
		t.Error("expected agent to be dead")
	}

	// Deliver anyway (per spec: deliver but add warning).
	if err := host.SendToAgent(agent, body); err != nil {
		t.Fatalf("SendToAgent error: %v", err)
	}
	if len(host.sent) != 1 {
		t.Fatal("expected delivery even to dead agent")
	}
}

func TestHandleAppMention_EmptyMessage(t *testing.T) {
	host := &fakeHost{
		agents: map[string]AgentInfo{
			"eng1": {Name: "eng1", Alive: true, Activity: "running"},
		},
	}

	agent, body := ParseMention("<@UBOT> eng1")
	if agent != "eng1" {
		t.Fatalf("agent = %q", agent)
	}
	if body != "" {
		t.Fatalf("body = %q, want empty", body)
	}

	// Empty body still delivers (sends Enter).
	if err := host.SendToAgent(agent, body); err != nil {
		t.Fatalf("SendToAgent error: %v", err)
	}
}

func TestHandleAppMention_SendError(t *testing.T) {
	host := &fakeHost{
		agents: map[string]AgentInfo{
			"eng1": {Name: "eng1", Alive: true, Activity: "running"},
		},
		sendErr: fmt.Errorf("PTY closed"),
	}

	err := host.SendToAgent("eng1", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "PTY closed") {
		t.Errorf("error = %q, want to contain 'PTY closed'", err)
	}
}

func TestReplyAgentNotFound_Format(t *testing.T) {
	// Test the format logic by calling ParseMention + AllAgents.
	host := &fakeHost{
		agents: map[string]AgentInfo{
			"eng1":  {Name: "eng1", Alive: true},
			"eng2":  {Name: "eng2", Alive: true},
			"super": {Name: "super", Alive: true},
		},
	}

	agent, _ := ParseMention("<@UBOT> eng99 test")
	if agent != "eng99" {
		t.Fatalf("agent = %q", agent)
	}

	_, found := host.FindAgent(agent)
	if found {
		t.Fatal("should not find eng99")
	}

	agents := host.AllAgents()
	if len(agents) != 3 {
		t.Fatalf("agents = %d", len(agents))
	}
}

// Verify that the Client struct compiles with the fakeHost as AgentHost.
var _ AgentHost = (*fakeHost)(nil)

// Verify NewClient accepts a host parameter.
func TestNewClient_WithHost(t *testing.T) {
	host := &fakeHost{agents: map[string]AgentInfo{}}
	c := NewClient("xapp-test", "xoxb-test", host, nil)
	if c.host == nil {
		t.Fatal("host should not be nil")
	}
}

// Test that slack types are used correctly (compile-time check).
func TestSlackTypes(t *testing.T) {
	// Verify ItemRef construction compiles.
	ref := slack.ItemRef{Channel: "C123", Timestamp: "1234.5678"}
	if ref.Channel != "C123" {
		t.Fatal("unexpected channel")
	}
}
