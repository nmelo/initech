package tui

// The inbox chime (ini-3wkl.5): a bell when an agent flags a post as worth
// interrupting the operator for, at most once per agent in a while.
//
// REUSES THE CHIMER AND THE WINDOW GATE, NEVER THE ATTENTION STATE MACHINE.
// The three lines of shared policy are the same ones attention_chime.go
// applies -- window 1 only, config off means silent, ring through the Chimer
// interface so a test can count it -- but chimeState is deliberately NOT
// touched. That struct is keyed to a WAITING EPISODE and carries
// chimeReminderDelay, the single 2-minute reminder. An inbox item chimes once
// and never reminds (operator's decision), so threading items through that
// bookkeeping would either inherit a reminder the operator rejected or need a
// "never remind" flag inside a machine built around reminding. Two consumers
// wanting different answers get two named call sites, never a union.
//
// ONCE PER HOST, not once per window (docs/spec.md §289): same-host windows
// share speakers, so per-window ringing multiplies bells rather than
// information. attention_chime.go's window-1 gate IS that rule on one host.
// CROSS-MACHINE HOSTS CURRENTLY NEVER RING and this bead does not change that
// -- the gap is ini-tagj's and is not claimed, asserted, or fixed here.

import (
	"fmt"
	"sync"
	"time"
)

// inboxChimeInterval is the per-agent rate limit (pm default, kept).
//
// SUPPRESSION IS OF THE BELL ONLY. The item always posts and always lists;
// this guard shapes the noise, not the inbox. That is the whole difference
// between it and a cap, and the spec is explicit there is no cap.
const inboxChimeInterval = 10 * time.Minute

// inboxChimeLimiter remembers when each agent last rang.
//
// IN MEMORY, ON WINDOW 1, and both halves are deliberate: a 10-minute window
// has no meaning across a fleet restart, and persisting it would write the
// store on every chime. It carries its own mutex because the post handler runs
// on the IPC goroutine -- the same reason the store does, and the same shape
// postTeachingState already uses.
type inboxChimeLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// allow reports whether this agent may ring now. When it may not, it returns
// how long ago the last chime was -- the limiter is the ONLY thing that knows
// that, which is why the teaching line is built here rather than by the
// caller.
func (l *inboxChimeLimiter) allow(agent string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if prev, ok := l.last[agent]; ok {
		if since := now.Sub(prev); since < inboxChimeInterval {
			return false, since
		}
	}
	if l.last == nil {
		l.last = make(map[string]time.Time)
	}
	l.last[agent] = now
	return true, 0
}

// chimeForInboxPost rings for a flagged item, and returns the teaching line
// when the limiter swallowed the bell.
//
// The returned notice travels back through child B's response channel, which
// is the one path guaranteed to land in the agent's context while it is
// paying attention.
func (t *TUI) chimeForInboxPost(id postIdentity, flagged bool, itemID string, now time.Time) (bool, string) {
	// An unflagged post never chimes. The operator's bell means "an agent
	// judged this worth interrupting you for"; ringing for everything is how
	// it stops meaning that.
	if !flagged {
		return false, ""
	}
	// Only window 1 makes noise. Every window RENDERS the item -- the sight
	// travels, the sound does not (§288/§289).
	if t.windowID != "" && t.windowID != WindowOne {
		return false, ""
	}
	// Config off: silent, and NO teaching line.
	//
	// Deliberate, and the reason matters: the suppressed line says "you chimed
	// 4m ago; at most one per 10 minutes", which is FALSE when the operator
	// simply turned the bell off. Teaching an agent to chime less because the
	// operator muted the speakers is a lie about the cause, and the agent
	// would act on it.
	if t.chime == nil || t.attentionSound == "none" {
		return false, ""
	}

	if ok, since := t.inboxChimes.allow(id.agent, now); !ok {
		if !t.postTeaching.once(id, "chime-suppressed") {
			return false, ""
		}
		return false, fmt.Sprintf("posted %s (chime suppressed — you chimed %s ago; at most "+
			"one per %s). Chime only when your work is stopped on the answer or something "+
			"is at risk.", itemID, roundChimeAge(since), inboxChimeInterval)
	}

	t.chime.Chime()
	return true, ""
}

// roundChimeAge renders the elapsed time the way the operator's line reads:
// whole minutes once there is a minute to report, seconds below that.
func roundChimeAge(d time.Duration) time.Duration {
	if d < time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(time.Minute)
}
