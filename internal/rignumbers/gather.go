package rignumbers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ReadBudget bounds each read of the repo, so a hung gh never holds a caller.
const ReadBudget = 10 * time.Second

// Runner runs one command and returns its standard output. Callers pass the real
// one (exec.CommandContext) or a fake in tests.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// slugRe reads owner/repo from a GitHub URL, https or ssh.
var slugRe = regexp.MustCompile(`github\.com[:/]([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)

// Slug returns the owner/repo of a GitHub URL, or false for anything else.
func Slug(url string) (string, bool) {
	m := slugRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", false
	}
	return m[1] + "/" + m[2], true
}

// GatherRepo reads the repo's numbers into in: the latest 100 commits on main
// and the push runs on main. Each read is bounded by ReadBudget. A read that
// fails leaves its Known flag false and returns its error, so the caller logs
// it and the numbers print unknown. With no slug it reads nothing.
func GatherRepo(ctx context.Context, run Runner, slug string, in *Input) error {
	if slug == "" {
		in.HasRepo = false
		return nil
	}
	in.HasRepo = true
	var errs []string
	cctx, cancel := context.WithTimeout(ctx, ReadBudget)
	out, err := run(cctx, "gh", "api", "repos/"+slug+"/commits?sha=main&per_page=100")
	cancel()
	if err == nil {
		in.Commits, err = parseCommits(out)
	}
	if err != nil {
		errs = append(errs, "commits: "+err.Error())
	} else {
		in.CommitsKnown = true
	}
	cctx, cancel = context.WithTimeout(ctx, ReadBudget)
	out, err = run(cctx, "gh", "run", "list", "-R", slug, "--branch", "main", "--event", "push",
		"--limit", "100", "--json", "conclusion,createdAt,updatedAt")
	cancel()
	if err == nil {
		in.Runs, err = parseRuns(out)
	}
	if err != nil {
		errs = append(errs, "runs: "+err.Error())
	} else {
		in.RunsKnown = true
	}
	if len(errs) > 0 {
		return fmt.Errorf("gh: %s", strings.Join(errs, "; "))
	}
	return nil
}

func parseCommits(b []byte) ([]Commit, error) {
	var raw []struct {
		Commit struct {
			Message   string `json:"message"`
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]Commit, 0, len(raw))
	for _, r := range raw {
		subject, _, _ := strings.Cut(r.Commit.Message, "\n")
		out = append(out, Commit{When: r.Commit.Committer.Date, Subject: subject})
	}
	return out, nil
}

func parseRuns(b []byte) ([]Run, error) {
	var raw []Run
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// BeadJSON is one bead as bd list --json gives it, the fields the numbers read.
type BeadJSON struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Assignee  string    `json:"assignee"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ClosedAt  time.Time `json:"closed_at"`
}

// GatherBeads reads every bead with one bd list, for a caller that has no bead
// poll of its own. A bead's last move is bd's updated_at.
func GatherBeads(ctx context.Context, run Runner, in *Input) error {
	cctx, cancel := context.WithTimeout(ctx, ReadBudget)
	defer cancel()
	out, err := run(cctx, "bd", "list", "--all", "--limit", "0", "--json")
	if err != nil {
		return fmt.Errorf("bd: %w", err)
	}
	var raw []BeadJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return fmt.Errorf("bd: %w", err)
	}
	in.Beads = in.Beads[:0]
	for _, b := range raw {
		in.Beads = append(in.Beads, Bead{ID: b.ID, Status: b.Status, Assignee: b.Assignee, CreatedAt: b.CreatedAt, ClosedAt: b.ClosedAt, LastMoved: b.UpdatedAt})
	}
	in.BeadsKnown = true
	return nil
}
