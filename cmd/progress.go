package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/progress"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var progressHours int

var progressCmd = &cobra.Command{
	Use:   "progress [<prefix>]",
	Short: "Show how an effort is moving: beads in flight, closes, the critical path, what quiet beads wait on",
	Long: `initech progress reads bd, main's commits and the agents' panes and prints,
for the beads whose id starts with <prefix> (an epic's children such as
ini-wra., or a molecule), or for the whole rig without one:

  a table of the beads past open and not closed: stage, owner, acceptance
  items proven (AC n/m, from "AC <n> proven" comments), commits on main in
  the window that name the bead, and the last move (a status change, a
  comment or a commit) and who made it;
  closed of total, split into beads an engineer worked and writing or
  review only; beads per stage; the closes in the window; the critical path
  (open beads with the most dependents); the engineers on it;
  for every bead with no move in an hour, what it waits on: "working,
  unrecorded" when its owner's pane is running or background, waiting on
  the thing its last comment names, else stalled.

It is read-only and never prints a finish time, an estimate or an actual.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runProgress,
}

func init() {
	progressCmd.Flags().IntVar(&progressHours, "hours", 6, "the window, in hours, for commits and closes")
	rootCmd.AddCommand(progressCmd)
}

// Seams for tests: the runner every read goes through, the clock, and the
// bound on the clone's fetch.
var (
	progressRunner       iexec.Runner = &iexec.DefaultRunner{}
	progressNow                       = time.Now
	progressFetchTimeout              = 10 * time.Second
)

// progressExitError is a failure that ends initech progress with exit 1
// and one line.
type progressExitError struct{ msg string }

func (e *progressExitError) Error() string { return e.msg }
func (e *progressExitError) ExitCode() int { return 1 }

// contextRunner is a Runner that can bound a command; DefaultRunner is one.
type contextRunner interface {
	RunContext(ctx context.Context, name string, args ...string) (string, error)
}

func runProgress(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if progressHours <= 0 {
		return fmt.Errorf("--hours must be at least 1")
	}
	prefix := ""
	if len(args) == 1 {
		prefix = args[0]
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found. Run 'initech init' first")
	}
	p, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	out := cmd.OutOrStdout()
	r := progressRunner
	now := progressNow().UTC()
	in := progress.Input{Rig: p.Name, Prefix: prefix, Hours: progressHours, Now: now}

	raw, err := r.RunInDir(p.Root, "bd", "list", "--all", "--limit", "0", "--json")
	var all []progress.Bead
	if err == nil {
		err = json.Unmarshal([]byte(raw), &all)
	}
	if err != nil {
		return &progressExitError{fmt.Sprintf("bd unreachable: %s", firstLine(err.Error()))}
	}
	for _, b := range all {
		if strings.HasPrefix(b.ID, prefix) {
			in.Beads = append(in.Beads, b)
		}
	}
	if len(in.Beads) == 0 {
		if prefix == "" {
			fmt.Fprintf(out, "no beads in %s\n", p.Name)
		} else {
			fmt.Fprintf(out, "no beads with prefix %s in %s\n", prefix, p.Name)
		}
		return nil
	}

	in.Comments = progressComments(r, p.Root, in.Beads, &in.Notes)
	since := now.Add(-time.Duration(progressHours) * time.Hour)
	in.Commits, in.CommitsKnown = progressCommits(r, p, since, &in.Notes)
	in.Panes, in.PanesKnown = progressPanes(p)
	if !in.PanesKnown {
		in.Notes = append(in.Notes, "owner panes not read (no initech answering): no quiet bead reads working")
	}
	fmt.Fprint(out, progress.Render(in))
	return nil
}

// progressComments reads the comments of every bead the report uses (those
// past open, and the closed ones for the engineer split), eight at a time.
func progressComments(r iexec.Runner, root string, beads []progress.Bead, notes *[]string) map[string][]progress.Comment {
	var ids []string
	for _, b := range beads {
		if b.Status != "open" {
			ids = append(ids, b.ID)
		}
	}
	out := make(map[string][]progress.Comment, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	failed := 0
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			raw, err := r.RunInDir(root, "bd", "comments", id, "--json")
			var cs []progress.Comment
			if err == nil {
				err = json.Unmarshal([]byte(raw), &cs)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				return
			}
			out[id] = cs
		}(id)
	}
	wg.Wait()
	if failed > 0 {
		*notes = append(*notes, fmt.Sprintf("comments not read for %d bead(s); their last move and AC count may be short", failed))
	}
	return out
}

// progressCommits reads main's commits in the window for every repo in
// initech.yaml: from the invoking agent's src clone when its origin is that
// repo, after a bounded fetch, else from the GitHub API. known is false when
// there is no repo to read.
func progressCommits(r iexec.Runner, p *config.Project, since time.Time, notes *[]string) (commits []progress.Commit, known bool) {
	if len(p.Repos) == 0 {
		return nil, false
	}
	clone := ""
	if agent := os.Getenv("INITECH_AGENT"); agent != "" {
		if d := filepath.Join(p.Root, agent, "src"); isDir(d) {
			clone = d
		}
	}
	read := 0
	for _, repo := range p.Repos {
		slug := repoSlug(repo.URL)
		if clone != "" {
			if origin, err := r.RunInDir(clone, "git", "remote", "get-url", "origin"); err == nil && slug != "" && repoSlug(strings.TrimSpace(origin)) == slug {
				if note := progressFetch(r, clone); note != "" {
					*notes = append(*notes, note)
				}
				out, err := r.RunInDir(clone, "git", "log", "origin/main", "--since="+since.Format(time.RFC3339), "--format=%cI%x09%s")
				if err == nil {
					commits = append(commits, parseCommits(out, "\t")...)
					read++
					continue
				}
			}
		}
		if slug == "" {
			*notes = append(*notes, fmt.Sprintf("commits not read for %s: cannot tell its GitHub owner/repo", repo.URL))
			continue
		}
		out, err := r.Run("gh", "api", fmt.Sprintf("repos/%s/commits?sha=main&since=%s&per_page=100", slug, since.Format("2006-01-02T15:04:05Z")),
			"--paginate", "--jq", `.[] | .commit.committer.date + " " + (.commit.message | split("\n")[0])`)
		if err != nil {
			*notes = append(*notes, fmt.Sprintf("commits not read for %s: %s", slug, firstLine(err.Error())))
			continue
		}
		commits = append(commits, parseCommits(out, " ")...)
		read++
	}
	return commits, read > 0
}

// progressFetch fetches origin/main into the clone, bounded by
// progressFetchTimeout; it returns a note when the fetch did not finish.
func progressFetch(r iexec.Runner, clone string) string {
	timedOut := "fetch of origin/main timed out; commits read from the clone as it stands"
	if cr, ok := r.(contextRunner); ok {
		ctx, cancel := context.WithTimeout(context.Background(), progressFetchTimeout)
		defer cancel()
		_, err := cr.RunContext(ctx, "git", "-C", clone, "fetch", "-q", "origin", "main")
		if ctx.Err() != nil {
			return timedOut
		}
		if err != nil {
			return "fetch of origin/main failed; commits read from the clone as it stands"
		}
		return ""
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.RunInDir(clone, "git", "fetch", "-q", "origin", "main")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return "fetch of origin/main failed; commits read from the clone as it stands"
		}
		return ""
	case <-time.After(progressFetchTimeout):
		return timedOut
	}
}

// parseCommits reads "<time><sep><subject>" lines.
func parseCommits(out, sep string) []progress.Commit {
	var cs []progress.Commit
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		d, subj, ok := strings.Cut(ln, sep)
		if !ok {
			continue
		}
		t, err := time.Parse(time.RFC3339, d)
		if err != nil {
			continue
		}
		cs = append(cs, progress.Commit{When: t, Subject: subj})
	}
	return cs
}

// repoSlug is "owner/repo" from a repos.url in any of the forms initech.yaml
// takes, "" when it has none.
func repoSlug(url string) string {
	u := strings.TrimSuffix(strings.TrimSpace(url), ".git")
	for _, pre := range []string{"https://", "http://", "ssh://"} {
		u = strings.TrimPrefix(u, pre)
	}
	u = strings.TrimPrefix(u, "git@")
	if i := strings.IndexAny(u, ":/"); i >= 0 {
		u = u[i+1:]
	} else {
		return ""
	}
	parts := strings.Split(strings.Trim(u, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(parts[len(parts)-2] + "/" + parts[len(parts)-1])
}

// progressPanes is each role's pane activity from the running initech;
// known is false when none answers.
func progressPanes(p *config.Project) (map[string]string, bool) {
	sock := os.Getenv("INITECH_SOCKET")
	if sock == "" {
		sock = tui.SocketPath(p.Root, p.Name)
	}
	resp, err := ipcCallSocket(sock, tui.IPCRequest{Action: "list"})
	if err != nil || resp == nil || !resp.OK {
		return nil, false
	}
	var panes []paneInfo
	if json.Unmarshal([]byte(resp.Data), &panes) != nil {
		return nil, false
	}
	out := make(map[string]string, len(panes))
	for _, pi := range panes {
		if pi.Host == "" {
			out[pi.Name] = pi.Activity
		}
	}
	return out, true
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
