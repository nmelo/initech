// releasepolicy decides whether a release may update GitHub Latest and Homebrew.
// GoReleaser invokes it automatically before building; maintenance releases still
// publish their assets, but only the highest stable remote tag may promote.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	initechexec "github.com/nmelo/initech/internal/exec"
)

func main() {
	tag := flag.String("tag", "", "release tag")
	output := flag.String("output", "", "promotion decision file")
	flag.Parse()
	if err := run(&initechexec.DefaultRunner{}, *tag, *output); err != nil {
		fmt.Fprintln(os.Stderr, "release promotion:", err)
		os.Exit(1)
	}
}

func run(r initechexec.Runner, tag, output string) error {
	if output == "" {
		return fmt.Errorf("output path is required")
	}
	// Invalidate a previous run before doing anything that can fail.
	if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
		return err
	}
	refs, err := r.Run("git", "ls-remote", "--tags", "--refs", "origin")
	if err != nil {
		return fmt.Errorf("read origin tags: %w", err)
	}
	promote, err := shouldPromote(tag, refs)
	if err != nil {
		return err
	}
	return os.WriteFile(output, []byte(strconv.FormatBool(promote)+"\n"), 0o600)
}

// Match full SemVer tags, then validate identifiers below. Core numbers stay
// strings: SemVer does not bound their size to a machine integer.
var tagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

func parseTag(tag string) []string {
	m := tagPattern.FindStringSubmatch(tag)
	if m == nil {
		return nil
	}
	for i := 4; i <= 5; i++ {
		if m[i] == "" {
			continue
		}
		for _, id := range strings.Split(m[i], ".") {
			if id == "" || (i == 4 && len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "") {
				return nil
			}
		}
	}
	return m
}

func shouldPromote(tag, refs string) (bool, error) {
	current := parseTag(tag)
	if current == nil {
		return false, fmt.Errorf("invalid release tag %q: expected vMAJOR.MINOR.PATCH", tag)
	}
	found, higher := false, false
	for _, line := range strings.Split(strings.TrimSpace(refs), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "refs/tags/") {
			return false, fmt.Errorf("invalid origin tag inventory line %q", line)
		}
		otherTag := strings.TrimPrefix(fields[1], "refs/tags/")
		found = found || otherTag == tag
		other := parseTag(otherTag)
		if other == nil || other[4] != "" {
			continue // Unrelated tags and prereleases do not outrank stable releases.
		}
		for i := 1; i <= 3; i++ {
			if other[i] == current[i] {
				continue
			}
			higher = higher || len(other[i]) > len(current[i]) || (len(other[i]) == len(current[i]) && other[i] > current[i])
			break
		}
	}
	if !found {
		return false, fmt.Errorf("release tag %q is missing from origin; refusing promotion with incomplete inventory", tag)
	}
	return current[4] == "" && !higher, nil
}
