// Package slackchat connects initech to Slack via Socket Mode. It receives
// @mention events from Slack and dispatches them to agent panes. The client
// is started as a goroutine from TUI.Run() when Slack tokens are configured.
package slackchat

import (
	"context"
	"log/slog"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// Client manages the Slack Socket Mode connection and event loop.
type Client struct {
	api           *slack.Client
	sm            *socketmode.Client
	host          AgentHost
	tracker       *ConversationTracker
	userCache     *UserCache
	threadContext bool            // When true, fetch thread history for dispatch context.
	allowedUsers  map[string]bool // Empty map = allow all.
	logger        *slog.Logger
}

// NewClient creates a Slack client configured for Socket Mode. The appToken
// (xapp-...) is used to establish the WebSocket connection. The botToken
// (xoxb-...) is used for Web API calls (posting messages, adding reactions).
// The host provides agent lookup and delivery; pass nil to defer wiring.
// allowedUsers is a list of Slack user IDs permitted to dispatch. Empty = all.
func NewClient(appToken, botToken string, host AgentHost, allowedUsers []string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	allowed := make(map[string]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	sm := socketmode.New(api)
	return &Client{
		api:           api,
		sm:            sm,
		host:          host,
		tracker:       NewConversationTracker(),
		userCache:     NewUserCache(api),
		threadContext: true,
		allowedUsers:  allowed,
		logger:        logger,
	}
}

// isAuthorized returns true if the user is allowed to dispatch commands.
// Returns true for all users when the allowed list is empty (default).
func (c *Client) isAuthorized(userID string) bool {
	if len(c.allowedUsers) == 0 {
		return true
	}
	return c.allowedUsers[userID]
}

// API returns the underlying Slack Web API client for use by the responder.
func (c *Client) API() *slack.Client { return c.api }

// Tracker returns the conversation tracker shared between dispatcher and responder.
func (c *Client) Tracker() *ConversationTracker { return c.tracker }

// SetThreadContext enables or disables thread history fetching for dispatch.
func (c *Client) SetThreadContext(enabled bool) { c.threadContext = enabled }

// Run connects to Slack via Socket Mode and processes events until the context
// is cancelled. It blocks, so call it in a goroutine. Reconnection is handled
// automatically by the slack-go library.
func (c *Client) Run(ctx context.Context) {
	go c.eventLoop(ctx)

	c.logger.Info("slack Socket Mode connecting")
	if err := c.sm.RunContext(ctx); err != nil {
		// RunContext returns when the context is cancelled (normal shutdown)
		// or on a fatal connection error.
		if ctx.Err() == nil {
			c.logger.Error("slack Socket Mode exited", "err", err)
		}
	}
}

// eventLoop reads events from the Socket Mode client and handles them.
// It runs concurrently with sm.RunContext which manages the WebSocket.
func (c *Client) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-c.sm.Events:
			if !ok {
				return
			}
			c.handleEvent(evt)
		}
	}
}

// handleEvent dispatches a Socket Mode event by type.
func (c *Client) handleEvent(evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		c.logger.Info("slack connecting")
	case socketmode.EventTypeConnected:
		c.logger.Info("slack connected")
	case socketmode.EventTypeConnectionError:
		c.logger.Warn("slack connection error")
	case socketmode.EventTypeEventsAPI:
		c.handleEventsAPI(evt)
	}
}

// handleEventsAPI processes Events API payloads delivered via Socket Mode.
// Currently handles app_mention events; all others are acknowledged and ignored.
func (c *Client) handleEventsAPI(evt socketmode.Event) {
	payload, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}

	// Acknowledge the event within Slack's 5-second window.
	c.sm.Ack(*evt.Request)

	switch ev := payload.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		c.logger.Info("slack mention received",
			"user", ev.User,
			"channel", ev.Channel,
			"text", ev.Text,
			"ts", ev.TimeStamp,
		)
		c.handleAppMention(ev)
	}
}
