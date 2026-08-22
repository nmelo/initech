package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTargetFixture(t *testing.T, makefile, ci, exempt string) (mk, ciPath, ex string) {
	t.Helper()
	dir := t.TempDir()
	mk = filepath.Join(dir, "Makefile")
	if err := os.WriteFile(mk, []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	ciPath = filepath.Join(dir, "ci.yml")
	if err := os.WriteFile(ciPath, []byte(ci), 0o644); err != nil {
		t.Fatal(err)
	}
	ex = filepath.Join(dir, "exempt.txt")
	if err := os.WriteFile(ex, []byte(exempt), 0o644); err != nil {
		t.Fatal(err)
	}
	return mk, ciPath, ex
}

const gatedCI = "jobs:\n  build:\n    steps:\n      - run: make check\n"

// The bead's headline: a target wired to nothing fails the build. This is the
// `make test-race` shape -- deliberately written, in no gate.
func TestTargetCensus_TargetNothingInvokesFails(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, "check:\n\techo hi\n\ntest-race:\n\tgo test -race ./...\n", gatedCI, "")
	err := runTargetCensus(mk, []string{ci}, ex, false)
	if err == nil {
		t.Fatal("a target nothing invokes passed the census; the whole tool is inert")
	}
	if !strings.Contains(err.Error(), "test-race") {
		t.Fatalf("failure does not name the uninvoked target: %v", err)
	}
}

func TestTargetCensus_TargetInvokedByCIIsCovered(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, "check:\n\techo hi\n", gatedCI, "")
	if err := runTargetCensus(mk, []string{ci}, ex, false); err != nil {
		t.Fatalf("a target CI invokes was reported uncovered: %v", err)
	}
}

func TestTargetCensus_PrerequisiteOfACoveredTargetIsCovered(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, "check: vet\n\techo hi\n\nvet:\n\tgo vet ./...\n", gatedCI, "")
	if err := runTargetCensus(mk, []string{ci}, ex, false); err != nil {
		t.Fatalf("a prerequisite of a covered target was reported uncovered: %v", err)
	}
}

func TestTargetCensus_TargetInvokedFromAnotherRecipeIsCovered(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, "check:\n\t$(MAKE) inner\n\ninner:\n\techo hi\n", gatedCI, "")
	if err := runTargetCensus(mk, []string{ci}, ex, false); err != nil {
		t.Fatalf("a target invoked from another recipe was reported uncovered: %v", err)
	}
}

// THE ECHO HAZARD. hooks-check prints "make install-hooks" as ADVICE. A scan
// that matched "make x" anywhere in a recipe would read help text as an
// invocation -- a checker counting documentation as coverage, which is the
// exact failure this tool exists to detect one level up.
func TestTargetCensus_RecipeThatMerelyPrintsMakeDoesNotCover(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t,
		"check:\n\t@echo \"then run: make install-hooks\"\n\ninstall-hooks:\n\tgit config core.hooksPath scripts/hooks\n",
		gatedCI, "")
	err := runTargetCensus(mk, []string{ci}, ex, false)
	if err == nil || !strings.Contains(err.Error(), "install-hooks") {
		t.Fatalf("a target merely NAMED in echoed help text was counted as invoked: %v", err)
	}
}

// The same hazard in the other inventory: a CI COMMENT mentioning a target is
// prose, not an invocation.
func TestTargetCensus_CommentMentioningMakeDoesNotCover(t *testing.T) {
	// The comment must be a line the invocation regex WOULD otherwise match --
	// "# run: make lint" contains the same run: anchor a real step does. A
	// fixture the regex rejects anyway (backticked prose) exercises nothing:
	// the first version of this test used one, and the mutant that disabled
	// the comment skip SURVIVED because the guard was never reached.
	ci := "jobs:\n  build:\n    steps:\n      # run: make lint   (we should do this one day)\n      - run: make check\n"
	mk, ciPath, ex := writeTargetFixture(t, "check:\n\techo hi\n\nlint:\n\tgolangci-lint run\n", ci, "")
	err := runTargetCensus(mk, []string{ciPath}, ex, false)
	if err == nil || !strings.Contains(err.Error(), "lint") {
		t.Fatalf("a target mentioned only in a CI COMMENT was counted as invoked: %v", err)
	}
}

func TestTargetCensus_ExemptionWithTriggerSatisfiesTheCensus(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, "check:\n\techo hi\n\nclean:\n\trm -f initech\n", gatedCI,
		"clean  # delete the binary. TRIGGER: by hand\n")
	if err := runTargetCensus(mk, []string{ci}, ex, false); err != nil {
		t.Fatalf("a documented exemption was rejected: %v", err)
	}
}

func TestTargetCensus_ExemptionWithoutTriggerIsRejected(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, "check:\n\techo hi\n\nclean:\n\trm -f initech\n", gatedCI,
		"clean  # delete the binary\n")
	err := runTargetCensus(mk, []string{ci}, ex, false)
	if err == nil || !strings.Contains(err.Error(), "run trigger") {
		t.Fatalf("a triggerless target exemption was accepted: %v", err)
	}
}

func TestTargetCensus_StaleExemptionFails(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, "check:\n\techo hi\n", gatedCI,
		"long-gone  # removed two releases ago. TRIGGER: never\n")
	err := runTargetCensus(mk, []string{ci}, ex, false)
	if err == nil || !strings.Contains(err.Error(), "long-gone") {
		t.Fatalf("a stale target exemption was not reported: %v", err)
	}
}

// .PHONY is a declaration, not a target, and must not be counted as one.
func TestTargetCensus_PhonyDeclarationIsNotATarget(t *testing.T) {
	mk, ci, ex := writeTargetFixture(t, ".PHONY: check\n\ncheck:\n\techo hi\n", gatedCI, "")
	if err := runTargetCensus(mk, []string{ci}, ex, false); err != nil {
		t.Fatalf(".PHONY was treated as an uninvoked target: %v", err)
	}
}

// The census must hold over the REAL repo, not only synthetic fixtures.
func TestTargetCensus_RepositoryInventoryIsFullyCovered(t *testing.T) {
	root := repoRoot(t)
	err := runTargetCensus(
		filepath.Join(root, "Makefile"),
		[]string{
			filepath.Join(root, ".github", "workflows", "ci.yml"),
			filepath.Join(root, ".github", "workflows", "release.yml"),
			filepath.Join(root, "scripts", "hooks", "pre-commit"),
		},
		filepath.Join(root, ".github", "make-target-exemptions.txt"),
		false,
	)
	if err != nil {
		t.Fatalf("live Makefile census failed:\n%v", err)
	}
}
