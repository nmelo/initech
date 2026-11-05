package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/rignumbers"
	"gopkg.in/yaml.v3"
)

// ini-61by: with product_repo set, the rig's repo numbers are that clone's,
// not the first repo's; a bad product_repo reads them unknown.

func productRig(t *testing.T, productRepo string) *config.Project {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "product", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var p config.Project
	body := "project: demo\nroot: " + root + "\nrepos:\n  - url: https://github.com/acme/scaffold\n    name: scaffold\n"
	if productRepo != "" {
		body += "product_repo: " + productRepo + "\n"
	}
	if err := yaml.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

// recordRun answers git for the product clone and gh for both repos, and
// records every command.
func recordRun(calls *[]string) rignumbers.Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		all := name + " " + strings.Join(args, " ")
		*calls = append(*calls, all)
		switch {
		case strings.Contains(all, " fetch "):
			return nil, nil
		case strings.Contains(all, " log origin/main"):
			return []byte("2026-11-05T15:00:00Z\tfeat: product\n"), nil
		case strings.Contains(all, "get-url origin"):
			return []byte("https://github.com/acme/product.git\n"), nil
		case strings.HasPrefix(all, "gh run list -R acme/product"), strings.HasPrefix(all, "gh run list -R acme/scaffold"):
			return []byte(`[]`), nil
		case strings.HasPrefix(all, "gh api repos/acme/scaffold/commits"):
			return []byte(`[]`), nil
		}
		return nil, errors.New("unexpected: " + all)
	}
}

// AC1: the first repo is scaffolding; product_repo names the product.
func TestGatherRigRepo_ProductRepoReadsTheProductNotTheFirstRepo(t *testing.T) {
	p := productRig(t, "product")
	var calls []string
	var in rignumbers.Input
	if err := GatherRigRepo(context.Background(), recordRun(&calls), p, rigSlug(p), &in); err != nil {
		t.Fatal(err)
	}
	if !in.CommitsKnown || len(in.Commits) != 1 || in.Commits[0].Subject != "feat: product" {
		t.Fatalf("commits = %+v", in.Commits)
	}
	dir, _ := p.ProductRepoDir()
	for _, c := range calls {
		if strings.Contains(c, "scaffold") {
			t.Fatalf("read the first repo with product_repo set: %q", c)
		}
	}
	if !strings.Contains(strings.Join(calls, "\n"), "git -C "+dir+" fetch") || !strings.Contains(strings.Join(calls, "\n"), "gh run list -R acme/product") {
		t.Fatalf("calls = %q", calls)
	}
}

// Feed it nothing: without product_repo the first repo is read, as today.
func TestGatherRigRepo_WithoutTheKeyTheFirstRepoIsRead(t *testing.T) {
	p := productRig(t, "")
	var calls []string
	var in rignumbers.Input
	_ = GatherRigRepo(context.Background(), recordRun(&calls), p, rigSlug(p), &in)
	got := strings.Join(calls, "\n")
	if !strings.Contains(got, "gh api repos/acme/scaffold/commits") || strings.Contains(got, "git ") {
		t.Fatalf("calls = %q, want today's first-repo reads", calls)
	}
}

// A product_repo that is not a clone reads the repo numbers unknown, and
// never falls back to the first repo.
func TestGatherRigRepo_ABadProductRepoIsUnknown(t *testing.T) {
	p := productRig(t, "nowhere")
	var calls []string
	var in rignumbers.Input
	err := GatherRigRepo(context.Background(), recordRun(&calls), p, rigSlug(p), &in)
	if err == nil || !strings.Contains(err.Error(), "product_repo") {
		t.Fatalf("err = %v", err)
	}
	if len(calls) != 0 || !in.HasRepo || in.CommitsKnown || in.RunsKnown {
		t.Fatalf("calls %q, input %+v: want nothing read and the numbers unknown", calls, in)
	}
	n := rignumbers.Compute(in)
	if !strings.Contains(n.Line(), "merges/h ?") {
		t.Fatalf("line %q, want merges/h ?", n.Line())
	}
}
