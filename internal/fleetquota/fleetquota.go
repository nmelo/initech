// Package fleetquota reads how much of its weekly cap the fleet has used,
// from the file cla writes on each poll of every Claude account
// (~/.claude-accounts/usage.json): the mean of each account's
// sevenDay.utilization, an integer percent. Every account has the same
// weekly cap, so the mean equals points used over the sum of caps.
//
// The file sits beside account credentials, so the reader decodes exactly two
// fields per account (sevenDay.utilization and polledAt) and drops the rest
// unread. Nothing here logs or returns an account name, the path, or any
// value beyond the quota itself.
package fleetquota

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StaleAfter is how old the newest poll may be and still count.
const StaleAfter = 10 * time.Minute

// ReadEvery is how often a Poller reads the file.
const ReadEvery = time.Minute

// DefaultPath is cla's usage file under the user's home.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude-accounts", "usage.json"), nil
}

// Quota is the fleet's weekly use. Known false is a dash, and Why says why
// in words that name no account and no path.
type Quota struct {
	Percent int
	Known   bool
	Why     string
}

// Cells is how many of the bar's four cells are full.
func (q Quota) Cells() int {
	if !q.Known {
		return 0
	}
	return int(math.Round(float64(q.Percent) / 25))
}

// account is everything decoded from one account: exactly the pinned fields.
type account struct {
	SevenDay *sevenDay `json:"sevenDay"`
	PolledAt string    `json:"polledAt"`
}

type sevenDay struct {
	Utilization *float64 `json:"utilization"`
}

// Read reads the file at path as of now.
func Read(path string, now time.Time) Quota {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Quota{Why: "the account usage file is missing"}
	}
	if err != nil {
		return Quota{Why: "the account usage file could not be read"}
	}
	var accounts map[string]account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return Quota{Why: "the account usage file could not be parsed"}
	}
	sum, n := 0.0, 0
	var newest time.Time
	for _, a := range accounts {
		if t, err := time.Parse(time.RFC3339Nano, a.PolledAt); err == nil && t.After(newest) {
			newest = t
		}
		if a.SevenDay != nil && a.SevenDay.Utilization != nil {
			sum += *a.SevenDay.Utilization
			n++
		}
	}
	if n == 0 {
		return Quota{Why: "no account in the usage file carries a weekly figure"}
	}
	if newest.IsZero() {
		// No account says when it was polled: the file's own time stands in.
		if fi, err := os.Stat(path); err == nil {
			newest = fi.ModTime()
		}
	}
	if now.Sub(newest) > StaleAfter {
		return Quota{Why: "the account usage file is stale (no poll in the last 10 minutes)"}
	}
	return Quota{Percent: int(math.Round(sum / float64(n))), Known: true}
}

// Poller reads the file at most once a minute and logs one line each time it
// becomes unreadable or stale, never every poll.
type Poller struct {
	path string
	log  func(string)
	read func(path string, now time.Time) Quota

	mu     sync.Mutex
	at     time.Time
	last   Quota
	logged string
	seen   bool
}

// NewPoller reads path; log may be nil.
func NewPoller(path string, log func(string)) *Poller {
	return &Poller{path: path, log: log, read: Read}
}

// Poll reads the file when a minute has passed since the last read, and
// returns the quota in force.
func (p *Poller) Poll(now time.Time) Quota {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen && now.Sub(p.at) < ReadEvery {
		return p.last
	}
	p.seen, p.at = true, now
	q := p.read(p.path, now)
	p.last = q
	switch {
	case q.Known:
		p.logged = ""
	case q.Why != p.logged:
		p.logged = q.Why
		if p.log != nil {
			p.log("fleet quota: " + q.Why + "; the bar shows a dash")
		}
	}
	return q
}

// Last is the quota from the last read, unknown before the first.
func (p *Poller) Last() Quota {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}
