// responder.go posts agent completion output back to Slack threads.
// It consumes AgentEvent copies from the TUI fan-out channel and posts
// threaded replies when a tracked agent completes work.
package slackchat

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// maxOutputChars is the maximum length of terminal output included in a Slack
// reply. Slack allows 40K but long messages are unreadable in threads.
const maxOutputChars = 2000

// rateLimitInterval is the minimum time between Slack posts for a given agent.
// Prevents rapid event sequences from flooding the thread.
const rateLimitInterval = 10 * time.Second

// ResponderEvent is the subset of TUI AgentEvent fields needed by the
// responder. Avoids importing the tui package.
type ResponderEvent struct {
	Type   string // "completed", "failed", "stuck", "idle"
	Pane   string // Agent name.
	BeadID string
	Detail string
}

// PanePeeker reads recent output from an agent's terminal.
type PanePeeker interface {
	// PeekOutput returns the last n lines from the named agent's terminal.
	// Returns empty string and nil error if the pane has no output.
	// Returns an error if the pane is not found.
	PeekOutput(agentName string, lines int) (string, error)
}

// ConversationLookup provides read access to the conversation tracker.
// The full tracker is owned by the Client; the responder only needs lookup.
type ConversationLookup interface {
	// Lookup returns the active conversation for the named agent, or false
	// if no conversation is tracked (expired or never started).
	Lookup(agent string) (channel string, threadTS string, ok bool)

	// Complete marks the conversation for the named agent as finished.
	// Safe to call if no conversation exists.
	Complete(agent string)
}

// Responder watches agent events and posts completion output to Slack threads.
type Responder struct {
	api      *slack.Client
	tracker  ConversationLookup
	peeker   PanePeeker
	mode     string // "completion" (default) or "off".
	logger   *slog.Logger
	mu       sync.Mutex
	lastPost map[string]time.Time // Per-agent rate limiter.
}

// NewResponder creates a responder. Mode is "completion" (default) or "off".
func NewResponder(api *slack.Client, tracker ConversationLookup, peeker PanePeeker, mode string, logger *slog.Logger) *Responder {
	if mode == "" {
		mode = "completion"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Responder{
		api:      api,
		tracker:  tracker,
		peeker:   peeker,
		mode:     mode,
		logger:   logger,
		lastPost: make(map[string]time.Time),
	}
}

// Run reads events from ch and posts Slack replies until ctx is cancelled.
func (r *Responder) Run(ctx context.Context, ch <-chan ResponderEvent) {
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			r.handleEvent(ev)
		case <-ctx.Done():
			return
		}
	}
}

// handleEvent processes a single agent event.
func (r *Responder) handleEvent(ev ResponderEvent) {
	if r.mode == "off" {
		return
	}

	switch ev.Type {
	case "completed":
		r.postCompletion(ev)
	case "failed":
		r.postFailure(ev)
	case "stuck":
		r.postWarning(ev)
	case "idle":
		r.postIdleCompletion(ev)
	}
}

// postCompletion posts agent output as a threaded code block reply.
func (r *Responder) postCompletion(ev ResponderEvent) {
	channel, threadTS, ok := r.tracker.Lookup(ev.Pane)
	if !ok {
		return
	}
	if !r.rateLimitOK(ev.Pane) {
		return
	}

	output := r.peekAgent(ev.Pane)
	header := fmt.Sprintf("*%s* finished", ev.Pane)
	if ev.BeadID != "" {
		header += fmt.Sprintf(" (`%s`)", ev.BeadID)
	}
	header += ":"

	msg := header + "\n" + formatOutput(output)
	r.post(channel, threadTS, msg)
}

// postIdleCompletion fires when an agent goes idle with a tracked conversation.
// Posts output and marks the conversation complete.
func (r *Responder) postIdleCompletion(ev ResponderEvent) {
	channel, threadTS, ok := r.tracker.Lookup(ev.Pane)
	if !ok {
		return
	}
	if !r.rateLimitOK(ev.Pane) {
		return
	}

	output := r.peekAgent(ev.Pane)
	header := fmt.Sprintf("*%s* is now idle", ev.Pane)
	if ev.BeadID != "" {
		header += fmt.Sprintf(" (`%s`)", ev.BeadID)
	}
	header += ":"

	msg := header + "\n" + formatOutput(output)
	r.post(channel, threadTS, msg)
	r.tracker.Complete(ev.Pane)
}

// postFailure posts a failure notice.
func (r *Responder) postFailure(ev ResponderEvent) {
	channel, threadTS, ok := r.tracker.Lookup(ev.Pane)
	if !ok {
		return
	}
	if !r.rateLimitOK(ev.Pane) {
		return
	}

	msg := fmt.Sprintf(":x: *%s* failed", ev.Pane)
	if ev.BeadID != "" {
		msg += fmt.Sprintf(" (`%s`)", ev.BeadID)
	}
	if ev.Detail != "" {
		msg += ": " + ev.Detail
	}
	r.post(channel, threadTS, msg)
}

// postWarning posts a stuck warning.
func (r *Responder) postWarning(ev ResponderEvent) {
	channel, threadTS, ok := r.tracker.Lookup(ev.Pane)
	if !ok {
		return
	}
	if !r.rateLimitOK(ev.Pane) {
		return
	}

	msg := fmt.Sprintf(":warning: *%s* appears stuck", ev.Pane)
	if ev.Detail != "" {
		msg += ": " + ev.Detail
	}
	r.post(channel, threadTS, msg)
}

// peekAgent reads the last 20 lines from the agent's terminal.
func (r *Responder) peekAgent(agent string) string {
	if r.peeker == nil {
		return ""
	}
	output, err := r.peeker.PeekOutput(agent, 20)
	if err != nil {
		r.logger.Warn("peek failed", "agent", agent, "err", err)
		return ""
	}
	return output
}

// formatOutput wraps terminal output in a Slack code block with truncation.
func formatOutput(output string) string {
	if output == "" {
		return "_No visible output_"
	}
	if len(output) > maxOutputChars {
		output = output[:maxOutputChars] + "\n... truncated (full output in TUI)"
	}
	return "```\n" + output + "\n```"
}

// post sends a threaded Slack message.
func (r *Responder) post(channel, threadTS, text string) {
	_, _, err := r.api.PostMessage(channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	)
	if err != nil {
		r.logger.Warn("slack responder post failed", "channel", channel, "err", err)
	}
}

// rateLimitOK returns true if enough time has passed since the last post
// for this agent. Updates the timestamp on success.
func (r *Responder) rateLimitOK(agent string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	last, ok := r.lastPost[agent]
	if ok && time.Since(last) < rateLimitInterval {
		return false
	}
	r.lastPost[agent] = time.Now()
	return true
}
