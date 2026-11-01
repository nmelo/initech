package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// mainPushOnlyGroup is the only concurrency group ci.yml and rigs.yml may use.
// Pushes to main resolve to one shared group per workflow, so a newer main push
// cancels the older run; every other run (release/** pushes, pull requests,
// schedule, dispatch) resolves to its own run id and is never cancelled or
// replaced. GitHub drops an older pending run in a shared group even with
// cancel-in-progress off, so a per-run group is the only way to never cancel.
const mainPushOnlyGroup = "${{ github.workflow }}-${{ (github.event_name == 'push' && github.ref == 'refs/heads/main') && 'main-push' || github.run_id }}"

// TestWorkflows_OnlyMainPushesShareAConcurrencyGroup fences the release gate
// against a cancelled run: a release-branch or tag run that a concurrency group
// could cancel leaves the cut with no CI or rig evidence. ci.yml and rigs.yml
// must carry exactly the main-push-only group. In ci.yml cancel-in-progress is
// off (ini-c5a.7, the operator via theguy): a started main run always
// finishes and a burst collapses to the newest pending sha. rigs.yml never
// runs on push, so its setting never fires. release.yml, which builds and
// publishes a tag, must carry no concurrency at all.
func TestWorkflows_OnlyMainPushesShareAConcurrencyGroup(t *testing.T) {
	root := repoRoot(t)
	type concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress bool   `yaml:"cancel-in-progress"`
	}
	read := func(name string) (*concurrency, int) {
		raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			Concurrency *concurrency           `yaml:"concurrency"`
			Jobs        map[string]interface{} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return wf.Concurrency, len(wf.Jobs)
	}
	for name, cancel := range map[string]bool{"ci.yml": false, "rigs.yml": true} {
		c, jobs := read(name)
		if jobs == 0 {
			t.Fatalf("%s: no jobs parsed -- the fence is reading nothing", name)
		}
		switch {
		case c == nil:
			t.Errorf("%s: no concurrency block; superseded main runs would queue on the self-hosted runners", name)
		case c.Group != mainPushOnlyGroup:
			t.Errorf("%s: concurrency group is %q; want %q so only main pushes share a group", name, c.Group, mainPushOnlyGroup)
		case c.CancelInProgress != cancel:
			t.Errorf("%s: cancel-in-progress is %v, want %v", name, c.CancelInProgress, cancel)
		}
	}
	if c, jobs := read("release.yml"); jobs == 0 {
		t.Fatal("release.yml: no jobs parsed -- the fence is reading nothing")
	} else if c != nil {
		t.Errorf("release.yml: has concurrency %+v; a tag's release run must never be cancelled or replaced", *c)
	}
}
