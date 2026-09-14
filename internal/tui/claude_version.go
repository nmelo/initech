package tui

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	iexec "github.com/nmelo/initech/internal/exec"
)

const claudeVersionTimeout = 500 * time.Millisecond

// Shared with the toast renderer, which reserves space for this diagnostic.
const undeliveredSubmitPrefix = "NOT delivered ("

// deliveredLateSubmitPrefix opens the belt's report that a withheld submit
// went out after all. Shared with the inbox, which confirms the reply on it
// (ini-33ma): a literal on each side is how the success report went unheard
// while the failure report, already shared, was mapped.
const deliveredLateSubmitPrefix = "delivered after the composer repainted ("

// One TUI/daemon session runs per process. Sharing this lazy cache covers all
// panes, including replacements, without adding any work to pane startup.
var sessionClaudeVersion = newClaudeVersionProbe((&iexec.DefaultRunner{}).RunContext)

type claudeVersionProbe struct {
	once    sync.Once
	done    chan struct{}
	running string // Published by closing done, immutable thereafter.
	run     func(context.Context, string, ...string) (string, error)
}

func newClaudeVersionProbe(run func(context.Context, string, ...string) (string, error)) *claudeVersionProbe {
	return &claudeVersionProbe{done: make(chan struct{}), run: run}
}

var claudeVersionNumber = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.+-]+)?$`)

// report is reached only after a submit is claimed as undelivered. It returns
// immediately even on the first failure; the existing report waits off-thread
// for at most the command deadline plus the runner's bounded pipe drain. Both
// lookup errors and successes are cached, so failures never cause retry storms.
func (p *claudeVersionProbe) report(emit func(string)) {
	p.once.Do(func() {
		go func() {
			defer close(p.done)
			ctx, cancel := context.WithTimeout(context.Background(), claudeVersionTimeout)
			defer cancel()
			out, err := p.run(ctx, "claude", "--version")
			if err != nil || ctx.Err() != nil {
				return
			}
			fields := strings.Fields(out)
			if len(fields) > 0 && claudeVersionNumber.MatchString(fields[0]) {
				p.running = fields[0]
			}
		}()
	})
	ready := func() {
		versions := "Claude measured against " + collapsedPasteMeasuredVersion
		if p.running != "" {
			versions += ", running " + p.running
		}
		emit(versions)
	}
	select {
	case <-p.done:
		ready()
	default:
		go func() { <-p.done; ready() }()
	}
}
