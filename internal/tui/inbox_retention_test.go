package tui

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// inboxRetentionValues reads the compiled field types and package constants,
// not a second enum list or a constant-name prefix. DeliveryStatus is an open
// string: all assignable string constants are a deliberately wider corpus
// than today's delivery vocabulary, supplemented with arbitrary text below.
func inboxRetentionValues(t *testing.T) (map[string]InboxState, map[string]string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	source, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range source.GoFiles {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	// Export paths come from the active module/build context, so dependencies
	// resolve on all supported platforms without assuming a GOPATH layout.
	exports, err := exec.Command("go", "list", "-deps", "-export", "-f", "{{.ImportPath}} {{.Export}}", ".").Output()
	if err != nil {
		t.Fatalf("discover type dependencies: %v", err)
	}
	paths := map[string]string{}
	for _, line := range strings.Split(string(exports), "\n") {
		path, export, ok := strings.Cut(line, " ")
		if ok && export != "" {
			paths[path] = export
		}
	}
	conf := types.Config{IgnoreFuncBodies: true, Importer: importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		export, ok := paths[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(export)
	})}
	pkg, err := conf.Check("github.com/nmelo/initech/internal/tui", fset, files, nil)
	if err != nil {
		t.Fatalf("read item types: %v", err)
	}
	obj := pkg.Scope().Lookup("InboxItem")
	if obj == nil {
		t.Fatal("InboxItem type disappeared")
	}
	item, ok := obj.Type().Underlying().(*types.Struct)
	if !ok {
		t.Fatal("InboxItem is not a struct")
	}
	var stateType, deliveryType types.Type
	for i := 0; i < item.NumFields(); i++ {
		field := item.Field(i)
		switch field.Name() {
		case "State":
			stateType = field.Type()
		case "DeliveryStatus":
			deliveryType = field.Type()
		}
	}
	if stateType == nil || deliveryType == nil {
		t.Fatal("item state/delivery fields disappeared")
	}
	states := map[string]InboxState{}
	deliveries := map[string]string{}
	for _, name := range pkg.Scope().Names() {
		c, ok := pkg.Scope().Lookup(name).(*types.Const)
		if !ok || c.Val().Kind() != constant.String {
			continue
		}
		value := constant.StringVal(c.Val())
		if types.Identical(c.Type(), stateType) {
			states[name] = InboxState(value)
		}
		if types.AssignableTo(c.Type(), deliveryType) {
			deliveries[name] = value
		}
	}
	if len(states) == 0 || len(deliveries) == 0 {
		t.Fatalf("empty type-derived inventory: states=%d deliveries=%d", len(states), len(deliveries))
	}
	return states, deliveries
}

// TestInboxRetention_RealStoreAndPanelAgree promotes qa2's ini-3wkl.4 probe:
// compare the panel before restart, actual items retained by LoadInbox, and
// the panel reading that same reloaded store. The independent literal oracle
// matters now that visibility is derived: equal answers can both be wrong.
func TestInboxRetention_RealStoreAndPanelAgree(t *testing.T) {
	states, deliveries := inboxRetentionValues(t)
	deliveries["zero value"] = ""
	// The field is an unrestricted string, including runtime failure details.
	// These are representatives, not a claim to enumerate an infinite domain.
	for i := 0; i < 8; i++ {
		deliveries[fmt.Sprintf("runtime detail %d", i)] = fmt.Sprintf("unconfirmed Ω failure detail %d\nretry later", i)
	}
	// The transition table is a second, runtime inventory: a discovery bug
	// that misses one declared state must not quietly shrink this test.
	for state := range inboxTransitions {
		found := false
		for _, declared := range states {
			if declared == state {
				found = true
			}
		}
		if !found {
			t.Errorf("transition state %q missing from type-derived inventory", state)
		}
	}
	t.Logf("type-derived corpus: %d states x %d delivery values", len(states), len(deliveries))
	stateNames := make([]string, 0, len(states))
	for name := range states {
		stateNames = append(stateNames, name)
	}
	sort.Strings(stateNames)
	deliveryNames := make([]string, 0, len(deliveries))
	for name := range deliveries {
		deliveryNames = append(deliveryNames, name)
	}
	sort.Strings(deliveryNames)
	root := inboxRoot(t)
	store := mustLoadInbox(t, root)
	retained := map[string]InboxItem{}
	for _, stateName := range stateNames {
		state := states[stateName]
		if _, accounted := inboxTransitions[state]; !accounted {
			t.Errorf("declared state %s (%q) has no transition-table accounting", stateName, state)
			continue
		}
		for _, deliveryName := range deliveryNames {
			delivery := deliveries[deliveryName]
			t.Run(stateName+"/"+deliveryName, func(t *testing.T) {
				// This switch is the independent expectation, NOT the fixture inventory.
				// New typed states are discovered above and must receive an explicit
				// retention decision here instead of silently inheriting a default.
				var wantRetained bool
				switch string(state) {
				case "unread", "seen":
					wantRetained = true
				case "answered":
					wantRetained = delivery != "delivered"
				case "dismissed", "withdrawn":
					wantRetained = false
				default:
					t.Fatalf("declared state %s (%q) has no retention contract", stateName, state)
				}
				id := mustPost(t, store, "eng3", "question", "retention-run").ID
				if state == InboxAnswered {
					if err := store.Answer(id, "operator reply"); err != nil {
						t.Fatal(err)
					}
				} else if state != InboxUnread {
					actor, reachable := inboxTransitions[InboxUnread][state]
					if !reachable {
						t.Fatalf("state %s needs a real-store transition path", stateName)
					}
					if err := store.Transition(id, state, actor); err != nil {
						t.Fatal(err)
					}
				}
				if err := store.SetDeliveryStatus(id, delivery); err != nil {
					t.Fatal(err)
				}
				item, ok := store.Item(id)
				if !ok || item.State != state || item.DeliveryStatus != delivery {
					t.Fatalf("fixture did not reach state=%q delivery=%q: %+v", state, delivery, item)
				}
				if inboxOpenForOperator(item) != wantRetained || inboxPrunable(item) == wantRetained {
					t.Errorf("predicates violate independent retention contract for state=%q delivery=%q", state, delivery)
				}
				if wantRetained {
					retained[id] = item
				}
			})
		}
	}
	// All discovered cells coexist: qa2's mixed-store probe, expanded from
	// four hand-picked items to the full type-derived matrix.
	assertInboxRetentionItems(t, "panel before reload", inboxListFor(store), retained)
	reloaded := mustLoadInbox(t, root)
	assertInboxRetentionItems(t, "store after reload", reloaded.Items(), retained)
	assertInboxRetentionItems(t, "panel after reload", inboxListFor(reloaded), retained)
	assertInboxRetentionItems(t, "store after second reload", mustLoadInbox(t, root).Items(), retained)
}

// Compare full item values as well as membership: preserving an ID while
// losing the operator's reply is still losing the answer.
func assertInboxRetentionItems(t *testing.T, where string, items []InboxItem, want map[string]InboxItem) {
	t.Helper()
	got := map[string]InboxItem{}
	for _, item := range items {
		got[item.ID] = item
	}
	if len(items) != len(got) {
		t.Errorf("%s: duplicate item IDs", where)
	}
	if len(got) != len(want) {
		t.Errorf("%s: got %d items, want %d", where, len(got), len(want))
	}
	for id, expected := range want {
		actual, ok := got[id]
		// YAML preserves the instant, not time.Time's monotonic clock or
		// location pointer. Normalize representation before comparing fields.
		actual.Created, actual.Seen, actual.Closed = actual.Created.UTC(), actual.Seen.UTC(), actual.Closed.UTC()
		expected.Created, expected.Seen, expected.Closed = expected.Created.UTC(), expected.Seen.UTC(), expected.Closed.UTC()
		if !ok || !reflect.DeepEqual(actual, expected) {
			t.Errorf("%s: retained item %s changed or disappeared: got %+v, want %+v", where, id, actual, expected)
		}
	}
	for id := range got {
		if _, ok := want[id]; !ok {
			t.Errorf("%s: unexpected item %s survived", where, id)
		}
	}
}

// A persisted state from a newer binary is already protected from pruning.
// Deriving visibility also keeps that retained item visible to the operator.
func TestInboxRetention_UnknownPersistedStateRemainsVisible(t *testing.T) {
	root := inboxRoot(t)
	data := "next_id: 2\nitems:\n  - id: p1\n    agent: eng3\n    body: question from a newer version\n    state: future-state-not-in-this-binary\n"
	if err := os.WriteFile(inboxPath(root), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	store := mustLoadInbox(t, root)
	item, ok := store.Item("p1")
	if !ok {
		t.Fatal("store pruned the unknown persisted state")
	}
	if item.State != "future-state-not-in-this-binary" {
		t.Fatalf("fixture state changed: %+v", item)
	}
	listed := inboxListFor(store)
	if len(listed) != 1 || listed[0].ID != "p1" {
		t.Fatalf("store retained the future item but panel hid it: %+v", listed)
	}
}
