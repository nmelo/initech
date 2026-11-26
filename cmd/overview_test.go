package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/spf13/cobra"
)

// The overview guard (ini-rko5). initech --help opens with one text that says
// the whole of what initech does; the README carries the same text, and a new
// super's charter points at it. These tests keep it true: a command or an
// agent type added or removed, or one copy edited without the other, turns CI
// red until the text follows.

// overviewCoverage places every visible top-level command under the bold
// heading of the overview bullet that covers it (pm's ruling, G1). lead1
// checks at the read that each placement is honest; a command no bullet
// covers goes to pm, never into a stretched bullet.
var overviewCoverage = map[string]string{
	"peek": "One screen for the team", "patrol": "One screen for the team", "status": "One screen for the team",
	"assign": "Work as beads", "deliver": "Work as beads", "bead": "Work as beads",
	"send": "Messages that never trample you", "dnd": "Messages that never trample you",
	"watch":     "Watches",
	"progress":  "Progress",
	"cron":      "Scheduled jobs",
	"strip":     "The list strip",
	"app-serve": "Your phone and tablet",
	"suspend":   "Care of the machine", "resume": "Care of the machine", "stop": "Care of the machine",
	"start": "Care of the machine", "restart": "Care of the machine", "compact": "Care of the machine",
	"clear": "Care of the machine", "down": "Care of the machine",
	"serve": "More than one machine", "remote": "More than one machine", "peers": "More than one machine",
	"reload": "More than one machine",
	"init":   "A team with roles", "add": "A team with roles", "remove": "A team with roles",
	"add-agent": "A team with roles", "delete-agent": "A team with roles", "whoami": "A team with roles",
}

// overviewPlumbing is the commands every cobra program has.
var overviewPlumbing = map[string]bool{"completion": true, "help": true, "version": true}

// notInOverview is the commands no bullet honestly covers, each with its
// reason (super's ruling): they are left out rather than stretch a bullet, and
// nothing is added to the approved text for them.
var notInOverview = map[string]string{
	"last":      "reads an agent's past messages from its session log; the messages bullet is about sending",
	"recording": "works with PTY recordings and their replay; no bullet speaks of recording or replay",
	"interrupt": "presses a key into the pane at once, on purpose; the messages bullet is about waiting until the pane can take one",
	"keystroke": "presses a key into the pane at once, on purpose; the messages bullet is about waiting until the pane can take one",
}

// agentTypeNames is how the overview names each agent type in internal/config.
var agentTypeNames = map[string]string{
	config.AgentTypeClaudeCode: "Claude Code",
	config.AgentTypeCodex:      "Codex",
	config.AgentTypeOpenCode:   "OpenCode",
	config.AgentTypeGeneric:    "generic",
}

// superPointer is the line a new super's charter carries.
const superPointer = "Asked about initech, what it is or does: run `initech --help` and answer from its overview, then add the rig's own state."

const (
	readmeOverviewStart = "<!-- overview:start -->\n"
	readmeOverviewEnd   = "\n<!-- overview:end -->"
)

// helpOverview is the opening of initech --help: everything before the line
// that says how to run the TUI.
func helpOverview(t *testing.T) string {
	t.Helper()
	long := rootCmd.Long
	i := strings.Index(long, "\n\nRunning initech with no subcommand")
	if i < 0 {
		t.Fatal("the help no longer has its 'Running initech with no subcommand' line to mark the overview's end")
	}
	return long[:i]
}

// visibleCommands is the names of the visible top-level commands of root.
func visibleCommands(root *cobra.Command) []string {
	var names []string
	for _, c := range root.Commands() {
		if c.Hidden {
			continue
		}
		names = append(names, c.Name())
	}
	sort.Strings(names)
	return names
}

// coverageProblems is what is wrong between the command tree and the text: a
// visible command placed nowhere, a placement under a heading the text lacks,
// and a placement for a command that does not exist.
func coverageProblems(commands []string, text string, coverage map[string]string, plumbing map[string]bool, left map[string]string) []string {
	var out []string
	exists := map[string]bool{}
	for _, name := range commands {
		exists[name] = true
		if plumbing[name] {
			continue
		}
		if reason, ok := left[name]; ok {
			if _, placed := coverage[name]; placed || strings.TrimSpace(reason) == "" {
				out = append(out, fmt.Sprintf("command %q is both left out and placed, or left out with no reason", name))
			}
			continue
		}
		heading, ok := coverage[name]
		if !ok {
			out = append(out, fmt.Sprintf("command %q is in no bullet of the overview: place it, or take it to pm", name))
			continue
		}
		if !strings.Contains(text, "**"+heading+".**") {
			out = append(out, fmt.Sprintf("command %q is placed under %q, a bullet the overview does not have", name, heading))
		}
	}
	for name := range coverage {
		if !exists[name] {
			out = append(out, fmt.Sprintf("the map places %q, which is not a visible command", name))
		}
	}
	for name := range left {
		if !exists[name] {
			out = append(out, fmt.Sprintf("the left-out list names %q, which is not a visible command", name))
		}
	}
	sort.Strings(out)
	return out
}

var initechWord = regexp.MustCompile("`initech ([a-z][a-z-]*)")

// namedCommandProblems is every `initech <word>` the text names that is not a
// command.
func namedCommandProblems(commands []string, text string) []string {
	exists := map[string]bool{}
	for _, c := range commands {
		exists[c] = true
	}
	var out []string
	for _, m := range initechWord.FindAllStringSubmatch(text, -1) {
		if !exists[m[1]] {
			out = append(out, fmt.Sprintf("the overview names `initech %s`, which is not a command", m[1]))
		}
	}
	return out
}

func TestOverview_EveryVisibleCommandIsCovered(t *testing.T) {
	for _, p := range coverageProblems(visibleCommands(rootCmd), helpOverview(t), overviewCoverage, overviewPlumbing, notInOverview) {
		t.Error(p)
	}
}

func TestOverview_EveryCommandItNamesExists(t *testing.T) {
	text := helpOverview(t)
	if !initechWord.MatchString(text) {
		t.Fatal("the overview names no `initech <command>`, so this check reads nothing")
	}
	for _, p := range namedCommandProblems(visibleCommands(rootCmd), text) {
		t.Error(p)
	}
}

// AC2: each direction fails on a planted case, through the same code.
func TestOverview_TheGuardFailsOnPlantedCases(t *testing.T) {
	text := "x\n- **Watches.** An agent hands `initech watch` a wait.\n"
	cover := map[string]string{"watch": "Watches"}
	if p := coverageProblems([]string{"watch", "newthing"}, text, cover, nil, nil); len(p) != 1 || !strings.Contains(p[0], "newthing") {
		t.Errorf("a command added to the tree but not the text: %v", p)
	}
	if p := namedCommandProblems([]string{"watch"}, text+"Run `initech nosuch` too.\n"); len(p) != 1 || !strings.Contains(p[0], "nosuch") {
		t.Errorf("a word in the text with no command: %v", p)
	}
	if p := coverageProblems([]string{"watch"}, text, map[string]string{"watch": "Watches", "gone": "Watches"}, nil, nil); len(p) != 1 || !strings.Contains(p[0], "gone") {
		t.Errorf("a placement for a command that no longer exists: %v", p)
	}
	if p := coverageProblems([]string{"watch"}, "nothing here", cover, nil, nil); len(p) != 1 {
		t.Errorf("a placement under a heading the text lacks: %v", p)
	}
}

func TestOverview_NamesEveryAgentType(t *testing.T) {
	text := helpOverview(t)
	for _, typ := range []string{config.AgentTypeClaudeCode, config.AgentTypeCodex, config.AgentTypeOpenCode, config.AgentTypeGeneric} {
		name, ok := agentTypeNames[typ]
		if !ok {
			t.Errorf("agent type %q has no name in the overview's map", typ)
			continue
		}
		if !strings.Contains(text, name) {
			t.Errorf("the overview does not name agent type %q (%q)", typ, name)
		}
	}
}

// The overview carries no figure that could go stale: not the version, not a
// timing.
func TestOverview_CarriesNoDigits(t *testing.T) {
	if d := regexp.MustCompile(`[0-9]`).FindString(helpOverview(t)); d != "" {
		t.Errorf("the overview carries a digit (%q): a version or a figure goes stale", d)
	}
}

func TestOverview_TheREADMEOpensWithTheSameText(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i, j := strings.Index(s, readmeOverviewStart), strings.Index(s, readmeOverviewEnd)
	if i < 0 || j < i {
		t.Fatal("the README has no overview between its markers")
	}
	if got := s[i+len(readmeOverviewStart) : j]; got != helpOverview(t) {
		t.Errorf("the README's overview differs from initech --help's:\nREADME:\n%s\nhelp:\n%s", got, helpOverview(t))
	}
}

func TestOverview_TheSuperCharterPointsAtIt(t *testing.T) {
	if !strings.Contains(roles.SuperTemplate, superPointer) {
		t.Errorf("the super charter template lacks the line %q", superPointer)
	}
}

// Feed it nothing: initech --help in a folder with no rig prints the overview
// and then the keys, as before.
func TestOverview_HelpWithNoRigPrintsTheOverviewAndTheKeys(t *testing.T) {
	t.Chdir(t.TempDir())
	var out strings.Builder
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"--help"})
	defer func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil) }()
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("initech --help with no rig: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, initechOverview) {
		t.Errorf("the help does not open with the overview:\n%.200s", got)
	}
	if i, j := strings.Index(got, initechOverview), strings.Index(got, "Keybindings:"); j < 0 || j < i {
		t.Error("the keys do not follow the overview")
	}
}
