package slackchat

import (
	"fmt"
	"strings"
	"sync"

	"github.com/slack-go/slack"
)

const (
	// maxThreadMessages is the maximum number of thread messages included
	// in the context block. Older messages are dropped.
	maxThreadMessages = 10

	// maxContextChars is the maximum total characters for the context block.
	// Oldest messages are dropped first to fit within this limit.
	maxContextChars = 4000
)

// SlackAPI abstracts the Slack Web API methods needed for thread context.
// The real implementation is *slack.Client; tests provide a fake.
type SlackAPI interface {
	GetConversationReplies(params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error)
	GetUserInfo(userID string) (*slack.User, error)
}

// UserCache caches Slack user ID to display name lookups. Thread-safe.
// User names don't change during a session, so no TTL is needed.
type UserCache struct {
	api   SlackAPI
	cache sync.Map // map[string]string
}

// NewUserCache creates a cache backed by the given Slack API client.
func NewUserCache(api SlackAPI) *UserCache {
	return &UserCache{api: api}
}

// DisplayName returns the display name for a Slack user ID. Falls back to
// the raw ID (e.g. "<@U123>") if the lookup fails.
func (c *UserCache) DisplayName(userID string) string {
	if v, ok := c.cache.Load(userID); ok {
		return v.(string)
	}

	user, err := c.api.GetUserInfo(userID)
	if err != nil {
		// Fall back to raw mention format.
		fallback := "<@" + userID + ">"
		c.cache.Store(userID, fallback)
		return fallback
	}

	name := user.Profile.DisplayName
	if name == "" {
		name = user.RealName
	}
	if name == "" {
		name = user.Name
	}
	c.cache.Store(userID, name)
	return name
}

// FetchThreadContext retrieves messages from a Slack thread and formats them
// as a context block. Returns an empty string if the thread has no messages
// or the API call fails. The current message (identified by the @mention)
// is excluded from the context since it will be sent as the body.
func FetchThreadContext(api SlackAPI, cache *UserCache, channel, threadTS, currentTS string) string {
	msgs, totalFetched, err := fetchReplies(api, channel, threadTS)
	if err != nil || len(msgs) == 0 {
		return ""
	}

	// Exclude the current message (the @mention being processed).
	filtered := make([]slack.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Timestamp != currentTS {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return ""
	}

	// totalFetched tracks how many messages existed before truncation to maxThreadMessages.
	// Subtract 1 for the current message we excluded.
	return formatContextBlock(filtered, cache, totalFetched-1)
}

// fetchReplies calls the Slack API to get thread messages. Returns at most
// maxThreadMessages (the last N), the total count before truncation, and any error.
func fetchReplies(api SlackAPI, channel, threadTS string) (msgs []slack.Message, total int, err error) {
	params := &slack.GetConversationRepliesParameters{
		ChannelID: channel,
		Timestamp: threadTS,
	}

	var all []slack.Message
	for {
		batch, hasMore, cursor, apiErr := api.GetConversationReplies(params)
		if apiErr != nil {
			return nil, 0, apiErr
		}
		all = append(all, batch...)
		if !hasMore || cursor == "" {
			break
		}
		params.Cursor = cursor
	}

	total = len(all)
	if len(all) > maxThreadMessages {
		all = all[len(all)-maxThreadMessages:]
	}
	return all, total, nil
}

// formatContextBlock formats thread messages into a text block with user
// names resolved. totalInThread is the total number of messages that existed
// before any truncation (used to compute the "[... N earlier messages]" count).
func formatContextBlock(msgs []slack.Message, cache *UserCache, totalInThread int) string {
	lines := make([]string, len(msgs))
	for i, m := range msgs {
		name := resolveMessageAuthor(m, cache)
		// Strip mention markup from text for cleaner context display.
		text := mentionRe.ReplaceAllString(m.Text, "")
		text = strings.TrimSpace(text)
		lines[i] = fmt.Sprintf("@%s: %s", name, text)
	}

	// Truncate oldest messages first to fit within char limit.
	return buildTruncatedBlock(lines, totalInThread)
}

// resolveMessageAuthor returns a display name for the message author.
// Bot messages use BotProfile.Name or Username; user messages resolve via cache.
func resolveMessageAuthor(m slack.Message, cache *UserCache) string {
	if m.BotID != "" {
		if m.BotProfile != nil && m.BotProfile.Name != "" {
			return m.BotProfile.Name
		}
		if m.Username != "" {
			return m.Username
		}
		return "bot"
	}
	if m.User != "" {
		return cache.DisplayName(m.User)
	}
	return "unknown"
}

// buildTruncatedBlock joins formatted lines into a context block, dropping
// the oldest lines if the total exceeds maxContextChars.
func buildTruncatedBlock(lines []string, totalMessages int) string {
	header := "[Slack thread context]"
	footer := "[End thread context]"
	dropped := totalMessages - len(lines)

	// Try with all lines first.
	for len(lines) > 0 {
		var sb strings.Builder
		sb.WriteString(header)
		sb.WriteByte('\n')
		if dropped > 0 {
			sb.WriteString(fmt.Sprintf("[... %d earlier messages]\n", dropped))
		}
		for _, line := range lines {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		sb.WriteString(footer)

		result := sb.String()
		if len(result) <= maxContextChars {
			return result
		}

		// Drop the oldest remaining line and increment dropped count.
		lines = lines[1:]
		dropped++
	}

	return ""
}
