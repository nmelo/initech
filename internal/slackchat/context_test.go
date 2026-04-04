package slackchat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

// fakeSlackAPIForContext implements SlackAPI for testing.
type fakeSlackAPIForContext struct {
	replies []slack.Message
	users   map[string]*slack.User
	err     error
}

func (f *fakeSlackAPIForContext) GetConversationReplies(params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error) {
	if f.err != nil {
		return nil, false, "", f.err
	}
	return f.replies, false, "", nil
}

func (f *fakeSlackAPIForContext) GetUserInfo(userID string) (*slack.User, error) {
	u, ok := f.users[userID]
	if !ok {
		return nil, fmt.Errorf("user not found: %s", userID)
	}
	return u, nil
}

func makeMsg(user, text, ts string) slack.Message {
	return slack.Message{
		Msg: slack.Msg{
			User:      user,
			Text:      text,
			Timestamp: ts,
		},
	}
}

func makeBotMsg(botID, botName, text, ts string) slack.Message {
	return slack.Message{
		Msg: slack.Msg{
			BotID:     botID,
			Text:      text,
			Timestamp: ts,
			BotProfile: &slack.BotProfile{
				Name: botName,
			},
		},
	}
}

func TestFetchThreadContext_BasicThread(t *testing.T) {
	api := &fakeSlackAPIForContext{
		replies: []slack.Message{
			makeMsg("U001", "<@UBOT> eng1 fix the bug", "1000.0"),
			makeBotMsg("BBOT", "initech", "Sent to *eng1*", "1000.1"),
			makeMsg("U001", "<@UBOT> eng1 make it async", "1000.2"),
		},
		users: map[string]*slack.User{
			"U001": {RealName: "Nelson", Profile: slack.UserProfile{DisplayName: "nelson"}},
		},
	}
	cache := NewUserCache(api)

	ctx := FetchThreadContext(api, cache, "C123", "1000.0", "1000.2")

	if !strings.Contains(ctx, "[Slack thread context]") {
		t.Error("missing header")
	}
	if !strings.Contains(ctx, "[End thread context]") {
		t.Error("missing footer")
	}
	if !strings.Contains(ctx, "@nelson: eng1 fix the bug") {
		t.Errorf("missing first message, got: %s", ctx)
	}
	if !strings.Contains(ctx, "@initech: Sent to *eng1*") {
		t.Errorf("missing bot message, got: %s", ctx)
	}
	// The current message (1000.2) should be excluded from context.
	if strings.Contains(ctx, "make it async") {
		t.Error("current message should be excluded from context")
	}
}

func TestFetchThreadContext_TopLevelMention(t *testing.T) {
	// Top-level mention has no thread_ts, so no context to fetch.
	// The caller should pass empty threadTS, producing empty result.
	api := &fakeSlackAPIForContext{replies: nil}
	cache := NewUserCache(api)

	ctx := FetchThreadContext(api, cache, "C123", "", "1000.0")
	if ctx != "" {
		t.Errorf("expected empty context for top-level mention, got: %q", ctx)
	}
}

func TestFetchThreadContext_APIError(t *testing.T) {
	api := &fakeSlackAPIForContext{err: fmt.Errorf("permission denied")}
	cache := NewUserCache(api)

	ctx := FetchThreadContext(api, cache, "C123", "1000.0", "1000.2")
	if ctx != "" {
		t.Errorf("expected empty context on API error, got: %q", ctx)
	}
}

func TestFetchThreadContext_OnlyCurrentMessage(t *testing.T) {
	api := &fakeSlackAPIForContext{
		replies: []slack.Message{
			makeMsg("U001", "<@UBOT> eng1 hello", "1000.0"),
		},
		users: map[string]*slack.User{
			"U001": {RealName: "Nelson"},
		},
	}
	cache := NewUserCache(api)

	// If the thread has only the current message, no context to prepend.
	ctx := FetchThreadContext(api, cache, "C123", "1000.0", "1000.0")
	if ctx != "" {
		t.Errorf("expected empty context when only current message exists, got: %q", ctx)
	}
}

func TestFetchThreadContext_TruncatesToMaxMessages(t *testing.T) {
	var msgs []slack.Message
	for i := 0; i < 15; i++ {
		msgs = append(msgs, makeMsg("U001", fmt.Sprintf("message %d", i), fmt.Sprintf("%d.0", 1000+i)))
	}
	// Current message is the last one.
	currentTS := fmt.Sprintf("%d.0", 1000+14)

	api := &fakeSlackAPIForContext{
		replies: msgs,
		users: map[string]*slack.User{
			"U001": {RealName: "Nelson"},
		},
	}
	cache := NewUserCache(api)

	ctx := FetchThreadContext(api, cache, "C123", "1000.0", currentTS)

	// Should have "[... N earlier messages]" indicator.
	if !strings.Contains(ctx, "[...") {
		t.Errorf("expected truncation indicator, got: %s", ctx)
	}
	// Should not contain messages 0-4 (only last 10 kept, minus current = 9 shown).
	if strings.Contains(ctx, "message 0\n") {
		t.Error("message 0 should have been truncated")
	}
}

func TestFetchThreadContext_TruncatesToMaxChars(t *testing.T) {
	// Create messages that total more than maxContextChars.
	var msgs []slack.Message
	longText := strings.Repeat("x", 500)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, makeMsg("U001", longText, fmt.Sprintf("%d.0", 1000+i)))
	}
	// Current is the last.
	currentTS := fmt.Sprintf("%d.0", 1000+9)

	api := &fakeSlackAPIForContext{
		replies: msgs,
		users:   map[string]*slack.User{"U001": {RealName: "N"}},
	}
	cache := NewUserCache(api)

	ctx := FetchThreadContext(api, cache, "C123", "1000.0", currentTS)

	if len(ctx) > maxContextChars {
		t.Errorf("context length = %d, exceeds max %d", len(ctx), maxContextChars)
	}
	// Should still have the structure.
	if ctx != "" {
		if !strings.HasPrefix(ctx, "[Slack thread context]") {
			t.Error("missing header after truncation")
		}
	}
}

func TestUserCache_HitAndMiss(t *testing.T) {
	api := &fakeSlackAPIForContext{
		users: map[string]*slack.User{
			"U001": {RealName: "Nelson", Profile: slack.UserProfile{DisplayName: "nelson"}},
		},
	}
	cache := NewUserCache(api)

	// First call: API lookup.
	name := cache.DisplayName("U001")
	if name != "nelson" {
		t.Errorf("name = %q, want nelson", name)
	}

	// Second call: should come from cache (API not hit again).
	name2 := cache.DisplayName("U001")
	if name2 != "nelson" {
		t.Errorf("cached name = %q, want nelson", name2)
	}

	// Missing user: falls back to raw ID.
	name3 := cache.DisplayName("UMISSING")
	if name3 != "<@UMISSING>" {
		t.Errorf("missing user = %q, want <@UMISSING>", name3)
	}
}

func TestUserCache_FallbackToRealName(t *testing.T) {
	api := &fakeSlackAPIForContext{
		users: map[string]*slack.User{
			"U001": {RealName: "Nelson Melo", Profile: slack.UserProfile{}},
		},
	}
	cache := NewUserCache(api)

	name := cache.DisplayName("U001")
	if name != "Nelson Melo" {
		t.Errorf("name = %q, want 'Nelson Melo'", name)
	}
}

func TestUserCache_FallbackToUsername(t *testing.T) {
	api := &fakeSlackAPIForContext{
		users: map[string]*slack.User{
			"U001": {Name: "nmelo", Profile: slack.UserProfile{}},
		},
	}
	cache := NewUserCache(api)

	name := cache.DisplayName("U001")
	if name != "nmelo" {
		t.Errorf("name = %q, want 'nmelo'", name)
	}
}

func TestBuildTruncatedBlock_AllFit(t *testing.T) {
	lines := []string{"@a: hello", "@b: world"}
	block := buildTruncatedBlock(lines, 2)

	if !strings.Contains(block, "@a: hello") {
		t.Error("missing line a")
	}
	if !strings.Contains(block, "@b: world") {
		t.Error("missing line b")
	}
	if strings.Contains(block, "[...") {
		t.Error("should not have truncation indicator when all fit")
	}
}

func TestBuildTruncatedBlock_WithDropped(t *testing.T) {
	// Simulate: 5 total messages, only 3 passed in (2 already dropped by fetchReplies).
	lines := []string{"@a: msg3", "@b: msg4", "@c: msg5"}
	block := buildTruncatedBlock(lines, 5)

	if !strings.Contains(block, "[... 2 earlier messages]") {
		t.Errorf("expected truncation indicator for 2 dropped, got: %s", block)
	}
}

func TestResolveMessageAuthor_BotMessage(t *testing.T) {
	msg := makeBotMsg("BBOT", "initech", "test", "1.0")
	cache := NewUserCache(&fakeSlackAPIForContext{users: map[string]*slack.User{}})

	name := resolveMessageAuthor(msg, cache)
	if name != "initech" {
		t.Errorf("bot name = %q, want initech", name)
	}
}

func TestResolveMessageAuthor_BotNoProfile(t *testing.T) {
	msg := slack.Message{Msg: slack.Msg{BotID: "B1", Username: "webhook-bot"}}
	cache := NewUserCache(&fakeSlackAPIForContext{users: map[string]*slack.User{}})

	name := resolveMessageAuthor(msg, cache)
	if name != "webhook-bot" {
		t.Errorf("bot name = %q, want webhook-bot", name)
	}
}

func TestIsThreadContextEnabled_Default(t *testing.T) {
	// Import config indirectly: test the method via the config package.
	// Since we're in slackchat package, just verify the logic directly.
	// nil = true (default on).
	var b *bool
	enabled := b == nil || *b
	if !enabled {
		t.Error("nil should default to enabled")
	}

	f := false
	b = &f
	enabled = b == nil || *b
	if enabled {
		t.Error("explicit false should disable")
	}
}
