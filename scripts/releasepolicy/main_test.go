package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	initechexec "github.com/nmelo/initech/internal/exec"
	"gopkg.in/yaml.v3"
)

func tagRefs(tags ...string) string {
	var lines []string
	for _, tag := range tags {
		lines = append(lines, strings.Repeat("a", 40)+"\trefs/tags/"+tag)
	}
	return strings.Join(lines, "\n")
}

func TestShouldPromote_StableSemverAcrossReleaseLines(t *testing.T) {
	for _, tt := range []struct {
		name, tag string
		others    []string
		want      bool
	}{
		{"newest maintenance", "v2.13.4", []string{"v2.13.3"}, true},
		{"older maintenance", "v2.13.5", []string{"v2.14.0"}, false},
		{"new minor", "v2.14.0", []string{"v2.13.5"}, true},
		{"numeric patch", "v2.13.9", []string{"v2.13.10"}, false},
		{"numeric minor", "v2.9.99", []string{"v2.10.0"}, false},
		{"numeric major", "v9.99.99", []string{"v10.0.0"}, false},
		{"unbounded numbers", "v2.13.4", []string{"v999999999999999999999999.0.0"}, false},
		{"higher prerelease", "v2.13.4", []string{"v2.14.0-rc.1"}, true},
		{"prerelease candidate", "v2.14.0-rc.1", []string{"v2.13.4"}, false},
		{"build metadata equal", "v2.13.4+build.1", []string{"v2.13.4+build.2"}, true},
		{"unrelated tags", "v2.13.4", []string{"release-backup", "v3", "v3.0", "v03.0.0"}, true},
		{"later lower tag cannot erase higher", "v2.13.5", []string{"v2.14.0", "v1.99.99"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shouldPromote(tt.tag, tagRefs(append(tt.others, tt.tag)...))
			if err != nil || got != tt.want {
				t.Fatalf("promote %s = %v, %v; want %v", tt.tag, got, err, tt.want)
			}
		})
	}
}

func TestShouldPromote_RejectsInvalidCandidateOrIncompleteInventory(t *testing.T) {
	for _, tag := range []string{"", "v2", "v2.13", "2.13.4", "v02.13.4", "v2.13.4-01", "v2.13.4-rc..1", "v2.13.4+build..1"} {
		if got, err := shouldPromote(tag, tagRefs(tag)); err == nil || got {
			t.Errorf("accepted invalid candidate %q: %v, %v", tag, got, err)
		}
	}
	for _, refs := range []string{"", tagRefs("v2.13.3"), "warning: remote unavailable", "abc refs/heads/main"} {
		if got, err := shouldPromote("v2.13.4", refs); err == nil || got {
			t.Errorf("accepted incomplete inventory %q: %v, %v", refs, got, err)
		}
	}
}

func TestRun_RemoteFailureInvalidatesPriorDecision(t *testing.T) {
	output := filepath.Join(t.TempDir(), "decision")
	if err := os.WriteFile(output, []byte("true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(&initechexec.FakeRunner{Err: errors.New("remote unavailable")}, "v2.13.4", output)
	if err == nil || !strings.Contains(err.Error(), "remote unavailable") {
		t.Fatalf("remote failure = %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("stale decision survived failure: %v", err)
	}
}

// This checks the actual configuration consumers, so deleting either setting or
// reversing only one decision is caught alongside the version comparison.
func TestReleaseConfig_HookDrivesLatestAndTapTogether(t *testing.T) {
	var config struct {
		Before  struct{ Hooks []string }
		Release struct {
			MakeLatest string `yaml:"make_latest"`
		}
		Brews []struct {
			SkipUpload string `yaml:"skip_upload"`
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Before.Hooks) != 1 || len(config.Brews) != 1 {
		t.Fatal("expected the promotion hook and one Homebrew publisher")
	}
	for _, tt := range []struct {
		tag, higher, latest, skip string
	}{
		{"v2.13.4", "v2.13.3", "true", "false"},
		{"v2.13.5", "v2.14.0", "false", "true"},
		{"v2.14.0-rc.1", "v2.13.4", "false", "true"},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			dir := t.TempDir()
			data := struct{ Tag string }{tt.tag}
			// GoReleaser's documented template functions, with reads rooted in
			// this fixture so concurrent tests never share decision files.
			funcs := template.FuncMap{
				"trim": strings.TrimSpace,
				"mustReadFile": func(path string) (string, error) {
					b, err := os.ReadFile(filepath.Join(dir, path))
					return string(b), err
				},
			}
			render := func(text string) (string, error) {
				tmpl, err := template.New("config").Funcs(funcs).Parse(text)
				if err != nil {
					return "", err
				}
				var out bytes.Buffer
				err = tmpl.Execute(&out, data)
				return out.String(), err
			}
			for _, text := range []string{config.Release.MakeLatest, config.Brews[0].SkipUpload} {
				if _, err := render(text); err == nil {
					t.Fatal("publisher accepted a missing promotion decision")
				}
			}
			hook, err := render(config.Before.Hooks[0])
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Fields(hook)
			wantArgs := []string{"go", "run", "./scripts/releasepolicy", "-tag", tt.tag, "-output", ".release-promotion-" + tt.tag}
			if !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("hook args = %v, want %v", args, wantArgs)
			}
			runner := &initechexec.FakeRunner{Output: tagRefs(tt.tag, tt.higher)}
			if err := run(runner, args[4], filepath.Join(dir, args[6])); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(runner.Calls, []string{"|git ls-remote --tags --refs origin"}) {
				t.Fatalf("must inventory all remote tags, got %v", runner.Calls)
			}
			for _, check := range []struct{ text, want string }{
				{config.Release.MakeLatest, tt.latest}, {config.Brews[0].SkipUpload, tt.skip},
			} {
				if got, err := render(check.text); err != nil || got != check.want {
					t.Errorf("publisher decision = %q, %v; want %q", got, err, check.want)
				}
			}
		})
	}
}

func TestReleaseConfig_SerializesPublishersAcrossTags(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct {
			Concurrency struct {
				Group  string
				Cancel bool `yaml:"cancel-in-progress"`
				Queue  string
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	c := workflow.Jobs["release"].Concurrency
	if c.Group == "" || strings.Contains(c.Group, "${{") || c.Cancel || c.Queue != "max" {
		t.Fatalf("release publishers must share a fixed concurrency group without cancellation: %+v", c)
	}
}
