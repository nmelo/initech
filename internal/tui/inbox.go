package tui

// The operator inbox store (ini-3wkl.2): items agents post for the operator,
// their state machine, and the two detections that are derivations over the
// store's own contents.
//
// WHY A SEPARATE FILE, NOT A FIELD IN fleet-state.yaml. persistentFleetState
// is sorted slices of pane keys: small, idempotent, rewritten whole on every
// hide/protect/pin. An inbox is a GROWING LOG carrying free text -- multi-line
// bodies, reply text, default text. Sharing the file would make every hide
// rewrite every item body, and one unparseable body would degrade
// hide/protect/pin to read-only through the shared fallback path. A question
// an agent asked must not be able to cost the operator the ability to hide a
// pane.
//
// MODELLED ON fleet_state.go FOR PERSISTENCE, NOT FOR CONCURRENCY, and the
// difference is deliberate. fleet_state.go has no mutex at all -- it is only
// ever touched from the main goroutine through runOnMain, so the main loop's
// own serialization is its lock. This store is reached from the IPC goroutine,
// which may WAIT where the main loop may not (ini-oxnl, the same split
// peekContentBlocking records: "a goroutine that exists to answer one request
// has no display to protect and may wait"). So it carries its own mutex across
// every read and every mutation, and it must never be routed through
// runOnMain. Copying fleet_state.go literally would have produced an
// unsynchronised store reached from a second goroutine.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// InboxState is an item's position in the state machine.
type InboxState string

const (
	InboxUnread    InboxState = "unread"
	InboxSeen      InboxState = "seen"
	InboxAnswered  InboxState = "answered"
	InboxDismissed InboxState = "dismissed"
	InboxWithdrawn InboxState = "withdrawn"
)

// InboxDelivered is the one delivery status that lets an answered item be
// pruned. Any other value -- including blank -- means the reply has not been
// confirmed into the agent's pane.
const InboxDelivered = "delivered"

// inboxPrunable reports whether a terminal item may be removed at startup.
//
// TERMINAL-BY-STATUS IS NOT TERMINAL-BY-DELIVERY (pm 1ad837b, from eng3's
// adversarial read, and it is a correction to what I shipped). An ANSWERED
// item whose delivery is not confirmed survives every prune and every down,
// because its stored reply may be the ONLY COPY of the operator's answer: the
// spec's own edge case says a reply to a dead or stopped agent is retrievable
// via --check when it returns, and "when it returns" can be a later session.
// Pruning it on the next startup destroys the answer before the agent it was
// written for could ever read it.
//
// Dismissed and withdrawn prune regardless -- nothing was ever sent for them.
// inboxOpenForOperator is this predicate's complement for declared states.
// Unknown persisted states retain their existing boundary: kept here, hidden
// by the panel. TestInboxRetention_RealStoreAndPanelAgree pins the real-store
// contract, and the adjacent unknown-state test pins that boundary.
func inboxPrunable(it InboxItem) bool {
	if !inboxTerminal(it.State) {
		return false
	}
	if it.State == InboxAnswered && it.DeliveryStatus != InboxDelivered {
		return false
	}
	return true
}

// inboxTerminal reports whether a state is terminal -- the states pruned at
// startup. Answered, dismissed and withdrawn are all "the operator or the
// agent is done with this"; only unread and seen are open.
func inboxTerminal(s InboxState) bool {
	return s == InboxAnswered || s == InboxDismissed || s == InboxWithdrawn
}

// inboxActor is who is performing a transition. The state machine is not
// symmetric: withdraw is the AGENT's act and nothing else may reach it, while
// seen/answered/dismissed are the operator's.
type inboxActor string

const (
	actorOperator inboxActor = "operator"
	actorAgent    inboxActor = "agent"
)

// inboxTransitions is the state machine, as data.
//
// AS A TABLE ON PURPOSE: the spec states five states and a handful of legal
// moves, and a table is the only shape where "what is forbidden" is as
// readable as "what is allowed". Every mutation goes through applyTransition,
// so a state the machine cannot reach cannot appear in the panel -- which is
// the user story's second half ("the inbox never shows a state that cannot
// happen").
var inboxTransitions = map[InboxState]map[InboxState]inboxActor{
	InboxUnread: {
		InboxSeen:      actorOperator,
		InboxAnswered:  actorOperator,
		InboxDismissed: actorOperator,
		InboxWithdrawn: actorAgent,
	},
	InboxSeen: {
		InboxAnswered:  actorOperator,
		InboxDismissed: actorOperator,
		InboxWithdrawn: actorAgent,
	},
	// Terminal states have no outgoing transitions. Listed explicitly rather
	// than omitted so a reader sees the decision instead of an absence.
	InboxAnswered:  {},
	InboxDismissed: {},
	InboxWithdrawn: {},
}

// InboxItem is one posted item.
type InboxItem struct {
	ID          string     `yaml:"id"`
	Agent       string     `yaml:"agent"`
	Body        string     `yaml:"body"`
	DefaultText string     `yaml:"default_text,omitempty"`
	Chime       bool       `yaml:"chime,omitempty"`
	State       InboxState `yaml:"state"`
	ReplyText   string     `yaml:"reply_text,omitempty"`

	// DeliveryStatus is the SEND PATH's own verdict on the reply, in its
	// vocabulary -- never the panel's word. Blank means unconfirmed, never
	// delivered. Child B formats it for --check, child D updates it; the
	// field lives here because the PRUNE depends on it (below).
	DeliveryStatus string    `yaml:"delivery_status,omitempty"`
	Created        time.Time `yaml:"created"`
	Seen           time.Time `yaml:"seen,omitempty"`
	Closed         time.Time `yaml:"closed,omitempty"`

	// RePostOfDismissed marks an item whose first line matches one the
	// operator dismissed. Persisted with the item because the operator's row
	// must still say so after a restart.
	RePostOfDismissed bool `yaml:"repost_of_dismissed,omitempty"`
}

// FirstLine is the item's first line -- what the operator's row shows and what
// the re-post detection compares.
func (it InboxItem) FirstLine() string {
	if i := strings.IndexByte(it.Body, '\n'); i >= 0 {
		return it.Body[:i]
	}
	return it.Body
}

// persistentInbox is the on-disk shape. A slice, not a map: the file is a log
// the operator may read, and insertion order is the order the panel shows.
type persistentInbox struct {
	Items  []InboxItem `yaml:"items,omitempty"`
	NextID int         `yaml:"next_id,omitempty"`
}

// Inbox is the store.
type Inbox struct {
	mu     sync.Mutex
	root   string
	items  []InboxItem
	nextID int

	// authority is the single-writer rule, carried by the store so it is
	// enforced at the MUTATION POINT rather than at call sites (the
	// mutateFleet rule, fleet_state.go:475). A secondary window holds a
	// non-authority store and cannot write one no matter which primitive it
	// reaches.
	authority bool

	// readOnly marks a FALLBACK store synthesized because the real file could
	// not be parsed. A corrupt file is not an absent file: writes are refused
	// so the operator's real items are never overwritten with an empty set.
	readOnly bool

	// memoryOnly marks a store with no project root: session-scoped, writes
	// ACCEPTED but not persisted. Without it a rootless TUI would be worse
	// than useless -- inboxPath("") is RELATIVE, so save() would drop a stray
	// .initech/inbox.yaml into whatever directory initech was launched from
	// and report success.
	memoryOnly bool

	// taught latches the teaching conditions. In memory only and keyed by the
	// agent's RUN, never its name: a restarted agent has fresh context and is
	// taught again, a long-running one is never re-taught on a new item.
	taught map[string]bool
}

// ErrInboxReadOnly is returned when a write is attempted against a fallback
// store synthesized from an unparseable file.
var ErrInboxReadOnly = errors.New("inbox is unreadable; items cannot be changed until .initech/inbox.yaml is repaired or deleted")

// ErrInboxNotAuthority is returned when a non-primary window attempts a write.
// Single-writer, per docs/spec.md §291: a viewer requests mutations through
// window 1 and never writes session state directly.
var ErrInboxNotAuthority = errors.New("only window 1 writes the inbox; secondary windows must send the request to window 1")

// ErrInboxNoSuchItem is returned for an unknown id.
var ErrInboxNoSuchItem = errors.New("no such inbox item")

// ErrInboxBadTransition is returned when the state machine forbids a move.
var ErrInboxBadTransition = errors.New("inbox item cannot make that transition")

// ErrInboxNotOwner is returned when an agent acts on an item it did not post.
var ErrInboxNotOwner = errors.New("that inbox item belongs to another agent")

// InboxNotPersistingPrefix is the header line child C renders when the store
// is degraded.
//
// DEFINED HERE, WHERE THE CONDITION LIVES, AND REFERENCED BY C RATHER THAN
// COPIED. A panel or rig that copies the string breaks invisibly when the
// string changes -- ini-6e97 was a whole bead of exactly that, twice in one
// day. A owns the condition and the reason; C owns the render; the string
// itself has one definition.
const InboxNotPersistingPrefix = "inbox not persisting: "

// inboxPath returns the full path to .initech/inbox.yaml.
func inboxPath(projectRoot string) string {
	return filepath.Join(layoutDir(projectRoot), "inbox.yaml")
}

func newInbox(root string, authority bool) *Inbox {
	return &Inbox{root: root, authority: authority, nextID: 1, taught: map[string]bool{}}
}

// newFallbackInbox builds the read-only store used when the real one cannot be
// parsed. It KEEPS the root -- reads and error messages want to know which
// project this is -- and relies on readOnly, not on a blank root, to prevent
// writes. A blank root would be actively worse: inboxPath("") is relative.
func newFallbackInbox(root string, authority bool) *Inbox {
	ib := newInbox(root, authority)
	ib.readOnly = true
	return ib
}

// LoadInbox reads the store and PRUNES TERMINAL ITEMS.
//
// PRUNE AT STARTUP, NOT AT SHUTDOWN, and the divergence from the spec's
// wording is deliberate. The spec says terminal items stay "until initech
// down, then clear"; cmd/down.go sends an IPC quit and removes no files, and a
// kill -9 would skip any shutdown prune entirely. Pruning on load gives the
// operator and the agent identical observable behaviour -- a terminal item is
// checkable for the life of the session and gone in the next one -- and is
// robust to a crash. A CRASH-RESTART PRUNES IDENTICALLY TO A CLEAN ONE; that
// is asserted, not assumed, so nobody files it as a bug.
//
// A missing store is a fresh session, not an error. A store that exists but
// cannot be parsed IS an error: treating corruption as "fresh" would present
// as a successful reset while discarding what agents posted.
func LoadInbox(projectRoot string, authority bool) (*Inbox, error) {
	ib := newInbox(projectRoot, authority)
	if projectRoot == "" {
		ib.memoryOnly = true
		return ib, nil
	}

	data, err := os.ReadFile(inboxPath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return ib, nil
		}
		return newFallbackInbox(projectRoot, authority), fmt.Errorf("read inbox: %w", err)
	}

	var pi persistentInbox
	if err := yaml.Unmarshal(data, &pi); err != nil {
		return newFallbackInbox(projectRoot, authority), fmt.Errorf("parse inbox: %w", err)
	}

	for _, it := range pi.Items {
		if !inboxPrunable(it) {
			ib.items = append(ib.items, it)
		}
	}
	ib.nextID = pi.NextID
	if ib.nextID < 1 {
		ib.nextID = 1
	}
	// The prune is a WRITE: without it the pruned items return on the next
	// load, and a store that says it pruned but did not is worse than one that
	// never pruned. A read-only or memory-only store skips it by save()'s own
	// rules, which is correct -- neither can persist anything.
	if len(ib.items) != len(pi.Items) {
		_ = ib.save()
	}
	return ib, nil
}

// PersistenceReason returns "" when the store persists normally, or the reason
// it does not.
//
// THE CONDITION IS OWNED HERE AND RENDERED BY C. An operator who posts,
// restarts, and finds an empty inbox has lost items silently; this is what
// makes that visible, and it is a surfaced condition rather than a log line
// because a log nobody reads is not a surface.
func (ib *Inbox) PersistenceReason() string {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	switch {
	case ib.readOnly:
		return "the file could not be read; items are not being saved and will be gone next session"
	case ib.memoryOnly:
		return "no project root; items are kept for this session only"
	default:
		return ""
	}
}

// save writes the store atomically. Checked here rather than at each call site
// so there is no path -- present or future -- by which an unreadable store
// overwrites the file it failed to read.
func (ib *Inbox) save() error {
	if ib.readOnly {
		return ErrInboxReadOnly
	}
	if ib.memoryOnly {
		return nil // Session-scoped: accepted, deliberately not persisted.
	}
	dir := layoutDir(ib.root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create .initech/: %w", err)
	}
	data, err := yaml.Marshal(&persistentInbox{Items: ib.items, NextID: ib.nextID})
	if err != nil {
		return fmt.Errorf("marshal inbox: %w", err)
	}
	if err := writeFileAtomic(inboxPath(ib.root), data, 0600); err != nil {
		return fmt.Errorf("write inbox: %w", err)
	}
	return nil
}

// mutate is the one write chokepoint: authority and read-only are enforced
// HERE, so no call site can bypass either by reaching a primitive directly.
func (ib *Inbox) mutate(apply func() error) error {
	if !ib.authority {
		return ErrInboxNotAuthority
	}
	if ib.readOnly {
		return ErrInboxReadOnly
	}
	if err := apply(); err != nil {
		return err
	}
	return ib.save()
}

// ── the state machine ────────────────────────────────────────────────

// applyTransition is the ONLY way an item's state changes.
//
// Both halves are guarded: a move the table does not list is refused, and a
// move listed for the other actor is refused. The second half is what keeps
// withdraw the agent's act -- an operator cannot withdraw, and an agent cannot
// answer or dismiss, no matter which entry point is reached.
func (ib *Inbox) applyTransition(it *InboxItem, to InboxState, by inboxActor, now time.Time) error {
	allowed, ok := inboxTransitions[it.State][to]
	if !ok {
		return fmt.Errorf("%w: %s -> %s", ErrInboxBadTransition, it.State, to)
	}
	if allowed != by {
		return fmt.Errorf("%w: %s -> %s is the %s's act, not the %s's",
			ErrInboxBadTransition, it.State, to, allowed, by)
	}
	it.State = to
	switch {
	case to == InboxSeen:
		it.Seen = now
	case inboxTerminal(to):
		it.Closed = now
	}
	return nil
}

func (ib *Inbox) findLocked(id string) (*InboxItem, error) {
	for i := range ib.items {
		if ib.items[i].ID == id {
			return &ib.items[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrInboxNoSuchItem, id)
}

// Transition moves an item, enforcing the table and the actor.
func (ib *Inbox) Transition(id string, to InboxState, by inboxActor) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.mutate(func() error {
		it, err := ib.findLocked(id)
		if err != nil {
			return err
		}
		return ib.applyTransition(it, to, by, time.Now())
	})
}

// Answer records the operator's reply and moves the item to answered.
//
// THE REPLY TEXT IS WRITTEN HERE, by the one path every reply takes. Seam 2's
// named failure mode is an accept key implemented as a special case that marks
// answered WITHOUT storing text -- --check then reports "answered:" with
// nothing after it, and it passes every panel test anyone writes. Child D's
// accept composes its text and comes through here.
func (ib *Inbox) Answer(id, reply string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.mutate(func() error {
		it, err := ib.findLocked(id)
		if err != nil {
			return err
		}
		if err := ib.applyTransition(it, InboxAnswered, actorOperator, time.Now()); err != nil {
			return err
		}
		it.ReplyText = reply
		return nil
	})
}

// Withdraw removes an item its own agent has resolved.
//
// OWNER CHECK AND TRANSITION UNDER ONE LOCK, at eng3's request while building
// child B. The alternative -- read the item, check the agent, then call
// Transition -- takes the lock twice with a gap in between, and in that gap the
// operator can answer or dismiss the item from the panel. The agent's withdraw
// would then apply to an item that had already been resolved, and the operator
// would watch a reply he had just sent turn into a withdrawal. One lock, one
// decision.
func (ib *Inbox) Withdraw(id, agent string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.mutate(func() error {
		it, err := ib.findLocked(id)
		if err != nil {
			return err
		}
		if it.Agent != agent {
			return fmt.Errorf("%w: %s belongs to %s", ErrInboxNotOwner, id, it.Agent)
		}
		return ib.applyTransition(it, InboxWithdrawn, actorAgent, time.Now())
	})
}

// ── posting, and the two store-side detections ───────────────────────

// InboxPostResult carries what the caller must tell the agent: the item's id
// and any teaching line. The line goes into the response notice field child B
// owns (seam 1) -- detection lives where the state lives, delivery is one
// mechanism in B.
type InboxPostResult struct {
	ID     string
	Notice string
}

// inboxOpenThreshold is the per-agent open-item count whose CROSSING teaches.
// pm default 5; the line fires on the crossing, never per post above it.
const inboxOpenThreshold = 5

// Post adds an item and returns any teaching line.
//
// runKey identifies the agent's PROCESS, not its name, and the latches are
// keyed by it: a restarted agent has fresh context and is taught again, a
// long-running one is never re-taught on a new item. Child B supplies it from
// the connection (binding constraint on ini-3wkl.3). An empty runKey collapses
// to a single run, so B can land before deciding -- but a runKey that does not
// change across a restart makes the reset-on-restart half of AC 15 fail
// silently, which is why B asserts it across a simulated restart.
func (ib *Inbox) Post(it InboxItem, runKey string) (InboxPostResult, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()

	var res InboxPostResult
	err := ib.mutate(func() error {
		it.ID = fmt.Sprintf("p%d", ib.nextID)
		ib.nextID++
		it.State = InboxUnread
		if it.Created.IsZero() {
			it.Created = time.Now()
		}
		it.RePostOfDismissed = ib.matchesDismissedLocked(it)
		ib.items = append(ib.items, it)
		res.ID = it.ID
		res.Notice = ib.teachLocked(it, runKey)
		return nil
	})
	if err != nil {
		return InboxPostResult{}, err
	}
	return res, nil
}

// inboxSimilarityKey normalises a first line for the re-post comparison:
// lowercased, whitespace RUNS COLLAPSED TO ONE SPACE, trimmed.
//
// ITS OWN NAME, NOT compactPromptText, and pm amended the spec to say so
// (1ad837b). The modal detector's normaliser drops ALL whitespace and keeps
// case: it serves a different consumer, and borrowing it makes "now here"
// match "nowhere", because with every space gone the two are the same string.
// That is a FALSE re-post marker, which tells the operator to ignore something
// he should read -- the exact harm this detection is supposed to avoid. Two
// consumers wanting different answers get two named functions.
//
// I shipped the borrowed version in b4b4a9b; this replaces it.
func inboxSimilarityKey(firstLine string) string {
	return strings.ToLower(strings.Join(strings.Fields(firstLine), " "))
}

// matchesDismissedLocked reports whether this post repeats one the operator
// DISMISSED.
//
// Nothing cleverer than the normaliser above for v1: a false "(re-post of a
// dismissed item)" tells the operator to ignore something he should READ,
// which is worse than a missed marker.
//
// DISMISSED ONLY, NEVER ANSWERED. An answered item was engaged with; repeating
// it is not the abuse this marks, and flagging it would teach the agent that
// asking again after an answer is misuse.
func (ib *Inbox) matchesDismissedLocked(candidate InboxItem) bool {
	want := inboxSimilarityKey(candidate.FirstLine())
	if want == "" {
		return false
	}
	for _, it := range ib.items {
		if it.State != InboxDismissed || it.Agent != candidate.Agent {
			continue
		}
		if inboxSimilarityKey(it.FirstLine()) == want {
			return true
		}
	}
	return false
}

// openCountLocked counts an agent's open (unread/seen) items.
func (ib *Inbox) openCountLocked(agent string) int {
	n := 0
	for _, it := range ib.items {
		if it.Agent == agent && !inboxTerminal(it.State) {
			n++
		}
	}
	return n
}

// teachLocked returns the teaching lines for whichever conditions this post
// triggered, at most once per condition per run.
//
// EVERY FIRED CONDITION GETS ITS LINE. The first version of this returned only
// one -- the re-post line "winning" over a simultaneous threshold crossing --
// and the reasoning was wrong in a way worth recording, because it reads
// plausible: the spec's anti-nagging principle is about repeating THE SAME
// rule, not about two DIFFERENT rules each firing once. AC 15 says each
// detected condition produces its line.
//
// The consequence was worse than a missing line (found by eng3 integrating B,
// measured before fixing): the suppressed condition was never latched, and
// because the threshold teaches on the CROSSING, the crossing had already
// passed by the next post. The agent was never told it crossed -- not on that
// post, not later, not in any run. A dropped teaching line is not a delayed
// one.
func (ib *Inbox) teachLocked(it InboxItem, runKey string) string {
	var lines []string
	if it.RePostOfDismissed && ib.latchLocked(runKey, it.Agent, "repost") {
		lines = append(lines, fmt.Sprintf("posted %s — this matches an item the operator "+
			"dismissed. Dismissed means no: go with your stated default rather than "+
			"re-asking.", it.ID))
	}
	// The CROSSING, not the level: only the post that takes the agent from
	// below the threshold to at-or-above it teaches. A later post at 7 open
	// items is already above and says nothing.
	//
	// A CONSEQUENCE WORTH SEEING, because it is a judgment call and not an
	// oversight: an agent that RESTARTS while already above the threshold is
	// not taught in its new run, because no crossing happens in that run. The
	// spec says "fires once per crossing, not per post above it", and the
	// crossing is the event. The alternative -- teach whenever a fresh run
	// posts while above -- reads as "you have 8 items" on every restart, which
	// is the nagging the latch exists to prevent.
	if n := ib.openCountLocked(it.Agent); n == inboxOpenThreshold+1 &&
		ib.latchLocked(runKey, it.Agent, "threshold") {
		lines = append(lines, fmt.Sprintf("posted %s — you now have %d items waiting on "+
			"the operator. This inbox is for what only the operator can decide or know; "+
			"status goes to your pane or super, and a question you can decide yourself "+
			"should state a default and proceed.", it.ID, n))
	}
	return strings.Join(lines, "\n")
}

// latchLocked reports whether this condition may still teach, and latches it.
func (ib *Inbox) latchLocked(runKey, agent, condition string) bool {
	k := runKey + "\x00" + agent + "\x00" + condition
	if ib.taught[k] {
		return false
	}
	ib.taught[k] = true
	return true
}

// SetDeliveryStatus records the send path's own verdict on a reply.
//
// Child D owns the values and when they change; the store owns only that the
// field moves through the same guarded write as everything else, because the
// PRUNE reads it -- a status written around the store would prune an answer
// the agent never got.
func (ib *Inbox) SetDeliveryStatus(id, status string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.mutate(func() error {
		it, err := ib.findLocked(id)
		if err != nil {
			return err
		}
		it.DeliveryStatus = status
		return nil
	})
}

// ── reads ────────────────────────────────────────────────────────────

// Items returns a copy of the store's items, oldest first.
func (ib *Inbox) Items() []InboxItem {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	out := make([]InboxItem, len(ib.items))
	copy(out, ib.items)
	return out
}

// Item returns one item by id.
func (ib *Inbox) Item(id string) (InboxItem, bool) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	it, err := ib.findLocked(id)
	if err != nil {
		return InboxItem{}, false
	}
	return *it, true
}

// OpenCount returns an agent's open-item count -- the number the operator's
// row shows next to the agent name.
func (ib *Inbox) OpenCount(agent string) int {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.openCountLocked(agent)
}

// AgentItems returns one agent's items, for --mine.
func (ib *Inbox) AgentItems(agent string) []InboxItem {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var out []InboxItem
	for _, it := range ib.items {
		if it.Agent == agent {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}
