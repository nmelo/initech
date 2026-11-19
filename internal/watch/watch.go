// Package watch keeps the watches agents hand to initech: what each waits
// for, why, until when, and how it ended. It stores them in one file and knows
// nothing about panes, sends or conditions; window 1 owns the only Store, and
// the condition checks, delivery and commands read and change it through the
// API here (see docs/design/watches.md).
package watch

import "time"

// Kind names a condition from a fixed list. A kind this build does not know is
// kept and listed, and never judged.
type Kind string

// The kinds of the first build.
const (
	KindBeadStatus    Kind = "bead_status"
	KindBeadComment   Kind = "bead_comment"
	KindDeadline      Kind = "deadline"
	KindSendDelivered Kind = "send_delivered"
	KindSendHeld      Kind = "send_held"
)

// State is where a watch is: waiting, or one of its three endings.
type State string

// The four states. A watch leaves Waiting exactly once.
const (
	Waiting   State = "waiting"
	Fired     State = "fired"
	Missed    State = "missed"
	Cancelled State = "cancelled"
)

// The bounds, named in the operator guide.
const (
	// MaxWaitingPerOwner is how many waiting watches one agent may hold;
	// ended ones do not count.
	MaxWaitingPerOwner = 20
	// MaxWhy is the longest reason, in characters, on one line.
	MaxWhy = 200
	// History is how long an ended watch stays listed after it is delivered
	// or cancelled.
	History = time.Hour
	// HeldTellSuper is how long a watch's line may be held before the
	// owner's super is told, once.
	HeldTellSuper = 30 * time.Minute
	// WriteTroubleTellSuper is how long writes of the file may keep failing
	// before the rig's super is told, once.
	WriteTroubleTellSuper = 5 * time.Minute
)

// Watch is one watch as stored.
type Watch struct {
	ID           string            `yaml:"id"`
	Owner        string            `yaml:"owner"`
	Kind         Kind              `yaml:"kind"`
	Params       map[string]string `yaml:"params,omitempty"`
	Why          string            `yaml:"why"`
	Deadline     time.Time         `yaml:"deadline"`
	HoldIfAsleep bool              `yaml:"hold_if_asleep,omitempty"`
	Set          time.Time         `yaml:"set"`
	State        State             `yaml:"state"`
	Ended        time.Time         `yaml:"ended,omitempty"`
	Detail       string            `yaml:"detail,omitempty"`
	// Late is true for an ending whose deadline or evidence came before
	// window 1 last started: it ended while initech was down.
	Late     bool     `yaml:"late,omitempty"`
	Delivery Delivery `yaml:"delivery,omitempty"`
}

// Delivery is what became of an ended watch's line. It stays zero until the
// watch ends.
type Delivery struct {
	Handed        time.Time `yaml:"handed,omitempty"`
	Receipt       string    `yaml:"receipt,omitempty"`
	Delivered     time.Time `yaml:"delivered,omitempty"`
	ToSuper       bool      `yaml:"to_super,omitempty"`
	SuperToldHeld bool      `yaml:"super_told_held,omitempty"`
}

// IsZero lets the file leave out an empty delivery.
func (d Delivery) IsZero() bool { return d == Delivery{} }

// ended reports whether w has left Waiting.
func (w *Watch) ended() bool { return w.State != Waiting }

// endingOwed reports whether w's line still has to go out: fired or missed,
// and neither handed to delivery nor sent to super.
func (w *Watch) endingOwed() bool {
	return (w.State == Fired || w.State == Missed) && w.Delivery.Handed.IsZero() && !w.Delivery.ToSuper
}

// historyFrom is when w's hour of history starts, zero while it is not over:
// a cancelled watch from its cancel, a fired or missed one from its delivery
// (or its handing to super), never before.
func (w *Watch) historyFrom() time.Time {
	switch {
	case w.State == Cancelled:
		return w.Ended
	case !w.Delivery.Delivered.IsZero():
		return w.Delivery.Delivered
	case w.Delivery.ToSuper:
		return w.Delivery.Handed
	}
	return time.Time{}
}
