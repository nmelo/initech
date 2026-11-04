package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
)

// progressWaitTimeout bounds one tag or run question.
var progressWaitTimeout = 10 * time.Second

// progressWorld answers a report's tag and run waits for the rig's first
// repo: one git ls-remote per tag and one gh run view per run, per report.
// Anything it cannot answer leaves the wait open and adds one note.
type progressWorld struct {
	r     iexec.Runner
	url   string // the first repo's clone URL
	slug  string // its GitHub owner/repo
	notes *[]string
}

// newProgressWorld is the world for p, or nil when p names no repo, and then a
// tag or a run wait stays open.
func newProgressWorld(r iexec.Runner, p *config.Project, notes *[]string) *progressWorld {
	if len(p.Repos) == 0 {
		return nil
	}
	return &progressWorld{r: r, url: p.Repos[0].URL, slug: repoSlug(p.Repos[0].URL), notes: notes}
}

func (w *progressWorld) run(name string, args ...string) (string, error) {
	if cr, ok := w.r.(contextRunner); ok {
		ctx, cancel := context.WithTimeout(context.Background(), progressWaitTimeout)
		defer cancel()
		return cr.RunContext(ctx, name, args...)
	}
	return w.r.Run(name, args...)
}

// TagExists asks the repo whether refs/tags/<name> exists.
func (w *progressWorld) TagExists(name string) (bool, error) {
	out, err := w.run("git", "ls-remote", "--tags", w.url, "refs/tags/"+name)
	if err != nil {
		*w.notes = append(*w.notes, fmt.Sprintf("tag %s not checked: %s", name, firstLine(err.Error())))
		return false, err
	}
	return strings.Contains(out, "refs/tags/"+name), nil
}

// RunCompleted asks GitHub whether run id has completed. A run GitHub does not
// know is not an error: exists is false.
func (w *progressWorld) RunCompleted(id string) (done, exists bool, err error) {
	if w.slug == "" {
		*w.notes = append(*w.notes, fmt.Sprintf("run %s not checked: cannot tell the repo's GitHub owner/repo", id))
		return false, true, fmt.Errorf("no slug")
	}
	out, err := w.run("gh", "run", "view", id, "--repo", w.slug, "--json", "status")
	if err != nil {
		if msg := strings.ToLower(err.Error() + out); strings.Contains(msg, "not found") || strings.Contains(msg, "could not find") {
			*w.notes = append(*w.notes, fmt.Sprintf("run %s does not exist in %s", id, w.slug))
			return false, false, nil
		}
		*w.notes = append(*w.notes, fmt.Sprintf("run %s not checked: %s", id, firstLine(err.Error())))
		return false, true, err
	}
	var v struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return false, true, err
	}
	return v.Status == "completed", true, nil
}
