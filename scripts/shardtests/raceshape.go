package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// raceRunner marks a recipe line or a workflow step that runs the race shards.
const raceRunner = "shardtests run -race"

// backgrounded matches a shell & that backgrounds a command (not &&, not a
// redirection such as 2>&1 or &>), or a parallel runner.
var backgrounded = regexp.MustCompile(`(^|[^&>])&([^&>]|$)|xargs\s+(-\S*P|--max-procs)|\bparallel\b`)

// raceShardShape checks that the race shards run one at a time: make test-race
// runs them in sequence, and rigs.yml runs them in one job. Running them
// together brings back the peak memory that one go test -race ./... had. It
// returns one line per problem, naming what would run together.
func raceShardShape(makefile, workflow []byte) []string {
	var probs []string
	prereqs, recipe, ok := makeTarget(string(makefile), "test-race")
	switch {
	case !ok:
		probs = append(probs, "Makefile has no test-race target: the race shards' sequence cannot be checked")
	default:
		if prereqs != "" {
			probs = append(probs, fmt.Sprintf("test-race depends on %q: make -j runs prerequisites together", prereqs))
		}
		if !strings.Contains(recipe, raceRunner) {
			probs = append(probs, "test-race does not run "+raceRunner+": the race shards are not what it runs")
		}
		for _, line := range strings.Split(recipe, "\n") {
			if strings.Contains(line, raceRunner) && backgrounded.MatchString(line) {
				probs = append(probs, fmt.Sprintf("test-race runs race shards in parallel: %q", strings.TrimSpace(line)))
			}
		}
	}

	var wf struct {
		Jobs map[string]struct {
			Strategy any `yaml:"strategy"`
			Steps    []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflow, &wf); err != nil {
		return append(probs, fmt.Sprintf("rigs.yml does not parse: %v", err))
	}
	var jobs []string
	for name, job := range wf.Jobs {
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "make test-race") || strings.Contains(s.Run, raceRunner) {
				jobs = append(jobs, name)
				if job.Strategy != nil {
					probs = append(probs, fmt.Sprintf("rigs.yml job %s runs the race shards under a strategy matrix, so its copies run together", name))
				}
				break
			}
		}
	}
	sort.Strings(jobs)
	switch {
	case len(jobs) == 0:
		probs = append(probs, "rigs.yml has no job running the race shards")
	case len(jobs) > 1:
		probs = append(probs, fmt.Sprintf("rigs.yml runs the race shards in %d jobs at once: %s", len(jobs), strings.Join(jobs, ", ")))
	}
	return probs
}

// makeTarget returns a target's prerequisites and recipe (its tab-indented
// lines, continuation lines joined), and whether the target exists.
func makeTarget(makefile, target string) (prereqs, recipe string, ok bool) {
	lines := strings.Split(makefile, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, target+":") {
			continue
		}
		prereqs = strings.TrimSpace(strings.TrimPrefix(l, target+":"))
		var b strings.Builder
		for _, r := range lines[i+1:] {
			if !strings.HasPrefix(r, "\t") {
				break
			}
			body := strings.TrimSpace(r)
			if strings.HasSuffix(body, "\\") {
				b.WriteString(strings.TrimSuffix(body, "\\") + " ")
				continue
			}
			b.WriteString(body + "\n")
		}
		return prereqs, b.String(), true
	}
	return "", "", false
}
