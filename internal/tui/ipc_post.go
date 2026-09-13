// ipc_post.go connects agent inbox commands to the authority's store. Store
// operations run on IPC goroutines; only the pane identity snapshot uses main.
package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// MaxInboxBodyBytes bounds a post, including the worst-case JSON escaping.
const MaxInboxBodyBytes = 16 * 1024

// MaxInboxDefaultBytes leaves room for a quoted accept reply in the send envelope.
const MaxInboxDefaultBytes = 8 * 1024

var errPostSuppliedIdentity = errors.New("supplied identity is refused; remove sender/target identity fields")

const inboxUnreadStatus = "unread — your reply will be delivered to your pane; no need to poll"
const inboxQuestionTeaching = "tip: say what you will do if no answer comes (--default \"...\") and keep working; the operator can accept it with one key."

// ValidateInboxPost applies the same body validation at the CLI and authority.
func ValidateInboxPost(body, defaultText string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("empty post body; usage: initech post \"text\" | --stdin | -f <file>")
	}
	if !utf8.ValidString(body) || !utf8.ValidString(defaultText) {
		return fmt.Errorf("post body and default must be valid UTF-8")
	}
	if len(body) > MaxInboxBodyBytes {
		return fmt.Errorf("post body too large (max %d bytes)", MaxInboxBodyBytes)
	}
	if len(defaultText) > MaxInboxDefaultBytes {
		return fmt.Errorf("post default too large (max %d bytes)", MaxInboxDefaultBytes)
	}
	return nil
}

func isPostAction(action string) bool {
	switch action {
	case "post", "post_check", "post_mine", "post_withdraw":
		return true
	}
	return false
}

// inboxState loads once per TUI, never once per query; loading a snapshot must
// never act as a new fleet session.
//
// The POINTER is guarded (ini-3wkl.7): a child window's refresh replaces the
// store on the main loop while the IPC goroutine and the render loop read it.
// On window 1 the store never changes identity; it rings the doorbell instead.
func (t *TUI) inboxState() *Inbox {
	t.inboxOnce.Do(func() {
		authority := t.isFleetAuthority()
		ib, err := LoadInbox(t.projectRoot, authority)
		if err != nil {
			LogWarn("inbox", "load failed; refusing writes", "err", err)
			ib = newFallbackInbox(t.projectRoot, authority)
		}
		if authority {
			ib.SetOnChange(t.ringInboxDoorbell)
		}
		t.inboxStoreMu.Lock()
		t.inboxStore = ib
		t.lastInboxRefresh = time.Now()
		t.inboxStoreMu.Unlock()
	})
	t.inboxStoreMu.Lock()
	defer t.inboxStoreMu.Unlock()
	return t.inboxStore
}

func (t *TUI) handleIPCPost(conn net.Conn, req IPCRequest, raw []byte) {
	if err := postPlatformError(runtime.GOOS); err != nil {
		writeIPCResponse(conn, IPCResponse{Error: err.Error()})
		return
	}
	identity, err := t.identifyPostConnection(conn)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: err.Error()})
		return
	}
	if err := validatePostRequest(req, raw); err != nil {
		response := IPCResponse{Error: err.Error()}
		if errors.Is(err, errPostSuppliedIdentity) && t.postTeaching.once(identity, "forged-identity") {
			response.Notices = []string{postIdentityRule}
		}
		writeIPCResponse(conn, response)
		return
	}
	writeIPCResponse(conn, t.applyPostRequest(req, identity, time.Now()))
}

// Only the actual post schema and the zero-valued legacy IPCRequest scaffolding
// are accepted. In particular JSON's default unknown-field tolerance must not
// silently accept an agent/name/as/run_key that this action cannot trust.
func validatePostRequest(req IPCRequest, raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("invalid inbox request: %w", err)
	}
	for key := range fields {
		switch key {
		case "action", "text", "item_id", "default_text", "chime", "target", "host", "lines", "enter", "prune":
		default:
			return fmt.Errorf("%w: field %q is not accepted", errPostSuppliedIdentity, key)
		}
	}
	if req.Target != "" || req.Host != "" || req.Lines != 0 || req.Enter || req.Prune {
		return errPostSuppliedIdentity
	}
	if len(req.ItemID) > 64 {
		return fmt.Errorf("item id too long (max 64 bytes)")
	}
	if req.Action == "post" {
		if req.ItemID != "" {
			return fmt.Errorf("new posts cannot supply an item id")
		}
	} else if req.Text != "" || req.DefaultText != "" || req.Chime {
		return fmt.Errorf("status and withdrawal requests cannot carry a body, default, or chime")
	}
	if (req.Action == "post_check" || req.Action == "post_withdraw") && strings.TrimSpace(req.ItemID) == "" {
		return fmt.Errorf("item_id is required")
	}
	if req.Action == "post_mine" && req.ItemID != "" {
		return fmt.Errorf("post_mine does not take an item id")
	}
	return nil
}

func (t *TUI) applyPostRequest(req IPCRequest, identity postIdentity, now time.Time) IPCResponse {
	if identity.agent == "" || identity.runKey == "" {
		return IPCResponse{Error: "could not identify your pane"}
	}
	ib := t.inboxState()
	switch req.Action {
	case "post":
		if err := ValidateInboxPost(req.Text, req.DefaultText); err != nil {
			return IPCResponse{Error: err.Error()}
		}
		result, err := ib.Post(InboxItem{Agent: identity.agent, Body: req.Text, DefaultText: req.DefaultText, Chime: req.Chime}, identity.runKey)
		if err != nil {
			return IPCResponse{Error: err.Error()}
		}
		resp := IPCResponse{OK: true, Data: "posted " + result.ID}
		if result.Notice != "" {
			resp.Notices = append(resp.Notices, result.Notice)
		}
		// The bell, and the line the limiter alone can write (ini-3wkl.5).
		// Suppression is of the SOUND only -- the item is already posted and
		// already lists, whatever this returns.
		if _, notice := t.chimeForInboxPost(identity, req.Chime, result.ID, now); notice != "" {
			resp.Notices = append(resp.Notices, notice)
		}
		if strings.HasSuffix(strings.TrimSpace(req.Text), "?") && strings.TrimSpace(req.DefaultText) == "" && t.postTeaching.once(identity, "question-without-default") {
			resp.Notices = append(resp.Notices, inboxQuestionTeaching)
		}
		return resp
	case "post_mine":
		return IPCResponse{OK: true, Data: formatInboxMine(ib.AgentItems(identity.agent))}
	case "post_check", "post_withdraw":
		item, ok := ib.Item(req.ItemID)
		if !ok {
			return IPCResponse{Error: "no item " + req.ItemID}
		}
		if item.Agent != identity.agent {
			return IPCResponse{Error: fmt.Sprintf("%s belongs to another agent; use --mine to list your items", item.ID)}
		}
		if req.Action == "post_withdraw" {
			if err := ib.Withdraw(item.ID, identity.agent); err != nil {
				// Re-read so a concurrent answer's state and text are named in the refusal.
				current, exists := ib.Item(item.ID)
				if exists && inboxTerminal(current.State) {
					return IPCResponse{Error: fmt.Sprintf("%s is already %s", item.ID, formatInboxStatus(current, now))}
				}
				return IPCResponse{Error: err.Error()}
			}
			return IPCResponse{OK: true, Data: "withdrawn " + item.ID}
		}
		resp := IPCResponse{OK: true, Data: formatInboxStatus(item, now)}
		if item.State == InboxUnread {
			if notice := t.postTeaching.check(identity, item.ID, now); notice != "" {
				resp.Notices = append(resp.Notices, notice)
			}
		}
		return resp
	default:
		return IPCResponse{Error: "unknown inbox action"}
	}
}

func formatInboxStatus(item InboxItem, now time.Time) string {
	switch item.State {
	case InboxUnread:
		return inboxUnreadStatus
	case InboxSeen:
		age := now.Sub(item.Seen)
		if age < 0 {
			age = 0
		}
		return "seen " + age.Round(time.Second).String()
	case InboxAnswered:
		status := item.DeliveryStatus
		if status == "" {
			status = "unconfirmed"
		}
		return "answered: " + item.ReplyText + " — delivery: " + status
	default:
		return string(item.State)
	}
}

// --mine is a bounded index, not an inline dump of every body and answer. An
// individual --check recovers the answer, so no stored reply is truncated here.
func formatInboxMine(items []InboxItem) string {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	total := len(items)
	if total == 0 {
		return "no items posted"
	}
	if len(items) > 50 {
		items = items[:50]
	}
	var lines []string
	for _, item := range items {
		lines = append(lines, item.ID+" "+string(item.State))
	}
	if total > len(items) {
		lines = append(lines, fmt.Sprintf("… and %d more — initech post --check <id>", total-len(items)))
	}
	return strings.Join(lines, "\n")
}

type postRunTeaching struct {
	taught map[string]bool
	checks map[string][]time.Time
}

type postTeachingState struct {
	mu   sync.Mutex
	runs map[postIdentity]*postRunTeaching
}

func (s *postTeachingState) runLocked(id postIdentity) *postRunTeaching {
	if s.runs == nil {
		s.runs = make(map[postIdentity]*postRunTeaching)
	}
	if s.runs[id] == nil {
		s.runs[id] = &postRunTeaching{taught: make(map[string]bool), checks: make(map[string][]time.Time)}
	}
	return s.runs[id]
}

func (s *postTeachingState) once(id postIdentity, condition string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runLocked(id)
	if r.taught[condition] {
		return false
	}
	r.taught[condition] = true
	return true
}

func (s *postTeachingState) check(id postIdentity, itemID string, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runLocked(id)
	if r.taught["repeated-check"] {
		return ""
	}
	times := r.checks[itemID]
	kept := times[:0]
	for _, at := range times {
		if !at.Before(now.Add(-5 * time.Minute)) {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	r.checks[itemID] = kept
	if len(kept) < 3 {
		return ""
	}
	r.taught["repeated-check"] = true
	r.checks = nil
	return fmt.Sprintf("unread — checked %d times in %s. The reply is delivered to your pane; stop polling and keep working.", len(kept), now.Sub(kept[0]).Round(time.Second))
}
