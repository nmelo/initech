package slackchat

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// mentionRe matches Slack's encoded <@USERID> prefix in message text.
var mentionRe = regexp.MustCompile(`<@[A-Z0-9]+>\s*`)

// AgentInfo describes a running agent for Slack replies.
type AgentInfo struct {
	Name     string
	Alive    bool
	Activity string
}

// AgentHost provides agent lookup and delivery for Slack dispatch.
// Implemented by the TUI bridge adapter.
type AgentHost interface {
	// FindAgent returns info for the named agent, or false if not found.
	FindAgent(name string) (AgentInfo, bool)

	// AllAgents returns info for all managed agents.
	AllAgents() []AgentInfo

	// SendToAgent delivers text to the named agent's PTY (with Enter).
	// Returns an error if the agent is not found.
	SendToAgent(name, text string) error
}

// ParseMention strips the <@BOTID> prefix from Slack message text and
// extracts the agent name (first word, lowercased) and message body (rest).
// Returns empty strings when the input has no content after the mention.
func ParseMention(text string) (agent, body string) {
	stripped := mentionRe.ReplaceAllString(text, "")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return "", ""
	}

	parts := strings.SplitN(stripped, " ", 2)
	agent = strings.ToLower(parts[0])
	if len(parts) > 1 {
		body = strings.TrimSpace(parts[1])
	}
	return agent, body
}

// handleAppMention processes an app_mention event: parses the agent name,
// validates it, delivers the message, and posts a threaded reply.
func (c *Client) handleAppMention(ev *slackevents.AppMentionEvent) {
	agent, body := ParseMention(ev.Text)

	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}

	switch agent {
	case "":
		c.replyHelp(ev.Channel, threadTS)
		return
	case "help":
		c.replyHelp(ev.Channel, threadTS)
		return
	case "status":
		c.replyStatus(ev.Channel, threadTS)
		return
	}

	// Access control: dispatch commands require authorization.
	// Special commands (help, status) are handled above and bypass this check.
	if !c.isAuthorized(ev.User) {
		c.logger.Info("slack dispatch denied",
			"user", ev.User,
			"channel", ev.Channel,
			"agent", agent,
		)
		c.reply(ev.Channel, threadTS, "You don't have permission to dispatch initech agents. Contact your workspace admin.")
		return
	}

	if c.host == nil {
		c.reply(ev.Channel, threadTS, "No agent host configured.")
		return
	}

	info, found := c.host.FindAgent(agent)
	if !found {
		c.replyAgentNotFound(ev.Channel, threadTS, agent)
		return
	}

	if err := c.host.SendToAgent(agent, body); err != nil {
		c.reply(ev.Channel, threadTS, fmt.Sprintf("Delivery failed: %s", err))
		return
	}

	// Track the conversation so the responder can post results to this thread.
	c.tracker.Track(agent, ev.Channel, threadTS, ev.User)

	msg := fmt.Sprintf("Sent to *%s*", agent)
	if !info.Alive {
		msg += "\n:warning: Warning: " + agent + " is not running. Message queued for when it restarts."
	}
	c.reply(ev.Channel, threadTS, msg)
	c.react(ev.Channel, ev.TimeStamp, "white_check_mark")
}

// reply posts a threaded message in the given channel.
func (c *Client) reply(channel, threadTS, text string) {
	_, _, err := c.api.PostMessage(channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		c.logger.Warn("slack reply failed", "channel", channel, "err", err)
	}
}

// react adds an emoji reaction to a message.
func (c *Client) react(channel, timestamp, emoji string) {
	ref := slack.ItemRef{Channel: channel, Timestamp: timestamp}
	if err := c.api.AddReaction(emoji, ref); err != nil {
		c.logger.Warn("slack reaction failed", "channel", channel, "err", err)
	}
}

// replyHelp posts usage information.
func (c *Client) replyHelp(channel, threadTS string) {
	text := "*Usage:*\n" +
		"`@initech <agent> <message>` — send message to agent\n" +
		"`@initech status` — show agent roster\n" +
		"`@initech help` — show this help"
	c.reply(channel, threadTS, text)
}

// replyStatus posts the current agent roster.
func (c *Client) replyStatus(channel, threadTS string) {
	if c.host == nil {
		c.reply(channel, threadTS, "No agent host configured.")
		return
	}

	agents := c.host.AllAgents()
	if len(agents) == 0 {
		c.reply(channel, threadTS, "No agents running.")
		return
	}

	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })

	var sb strings.Builder
	sb.WriteString("*Agent Status:*\n")
	for _, a := range agents {
		status := ":large_green_circle:"
		if !a.Alive {
			status = ":red_circle:"
		}
		sb.WriteString(fmt.Sprintf("%s `%s` — %s\n", status, a.Name, a.Activity))
	}
	c.reply(channel, threadTS, sb.String())
}

// replyAgentNotFound posts an error with the list of active agents.
func (c *Client) replyAgentNotFound(channel, threadTS, agent string) {
	agents := c.host.AllAgents()
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	sort.Strings(names)
	c.reply(channel, threadTS,
		fmt.Sprintf("Agent `%s` not found. Active agents: %s", agent, strings.Join(names, ", ")))
}
