package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The shared writer orders the saves of one file (ini-5xcb). No race exists
// in the stores today (lead1's cause read: every caller is serialized), so
// these tests guard future callers; each is red against its mutant.

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An older save that reaches the writer after a newer one has landed is
// dropped; in order, the newer lands last as always.
func TestWriteFileAtomic_AnOlderSaveNeverReplacesANewerOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	older, newer := takeSaveTicket(path), takeSaveTicket(path)
	if err := writeFileAtomic(newer, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(older, []byte("older"), 0o600); err != nil {
		t.Fatalf("a dropped older save must report success: %v", err)
	}
	if got := readFile(t, path); got != "newer" {
		t.Errorf("file holds %q, want the newer save", got)
	}
	// The twin: in order, each lands.
	a, b := takeSaveTicket(path), takeSaveTicket(path)
	_ = writeFileAtomic(a, []byte("a"), 0o600)
	if got := readFile(t, path); got != "a" {
		t.Errorf("an in-order save did not land: %q", got)
	}
	_ = writeFileAtomic(b, []byte("b"), 0o600)
	if got := readFile(t, path); got != "b" {
		t.Errorf("the newer in-order save did not land: %q", got)
	}
	// A dropped save leaves no staging file behind.
	if left, _ := filepath.Glob(path + ".tmp-*"); len(left) != 0 {
		t.Errorf("staging files left behind: %v", left)
	}
}

// A store's shape: its content and ticket are taken together under its lock;
// the writes happen later, released in reverse. The file holds the newest
// content, and with it every change (pm, condition 3). Repeated, with the
// interleaving injected through channels and no clock.
func TestWriteFileAtomic_SavesReleasedInReverseKeepEveryChange(t *testing.T) {
	for rep := 0; rep < 200; rep++ {
		path := filepath.Join(t.TempDir(), "state.yaml")
		var mu sync.Mutex
		var state []string
		const n = 4
		release := make([]chan struct{}, n)
		taken := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			release[i] = make(chan struct{})
			// Each change is applied, then its content and ticket taken, in order.
			mu.Lock()
			state = append(state, fmt.Sprintf("change-%d", i))
			ticket := takeSaveTicket(path)
			content := strings.Join(state, ",")
			mu.Unlock()
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				taken <- struct{}{}
				<-release[i]
				if err := writeFileAtomic(ticket, []byte(content), 0o600); err != nil {
					t.Error(err)
				}
			}(i)
			<-taken
		}
		for i := n - 1; i >= 0; i-- { // newest first
			close(release[i])
		}
		wg.Wait()
		if got, want := readFile(t, path), "change-0,change-1,change-2,change-3"; got != want {
			t.Fatalf("rep %d: file holds %q, want every change %q", rep, got, want)
		}
	}
}

// The strip book through its API: two changes in order, then many at once;
// the file holds them all.
func TestStripBook_EveryChangeIsOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strips.yaml")
	b := loadStripBook(path, wmT0)
	if err := b.set("eng1", []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if err := b.set("eng2", []string{"two"}); err != nil {
		t.Fatal(err)
	}
	if got := loadStripBook(path, wmT0).all(); len(got) != 2 {
		t.Fatalf("after two changes the file holds %v, want both", got)
	}
	for rep := 0; rep < 50; rep++ {
		path := filepath.Join(t.TempDir(), "strips.yaml")
		b := loadStripBook(path, wmT0)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if err := b.set(fmt.Sprintf("eng%d", i), []string{"item"}); err != nil {
					t.Error(err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		if got := loadStripBook(path, wmT0).all(); len(got) != 8 {
			t.Fatalf("rep %d: eight changes at once left %d on disk", rep, len(got))
		}
	}
}

// The inbox through its API: posts from many goroutines at once all reach
// the file, numbered without a gap.
func TestInbox_PostsAtOnceAreAllOnDisk(t *testing.T) {
	for rep := 0; rep < 50; rep++ {
		root := t.TempDir()
		ib, err := LoadInbox(root, true)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if _, err := ib.Post(InboxItem{Agent: "eng2", Body: fmt.Sprintf("post %d", i)}, ""); err != nil {
					t.Error(err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		again, err := LoadInbox(root, true)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, it := range again.Items() {
			ids = append(ids, it.ID)
		}
		sort.Strings(ids)
		if len(ids) != 8 {
			t.Fatalf("rep %d: eight posts at once left %d on disk: %v", rep, len(ids), ids)
		}
	}
}

// syncCall is a call that forces a file to disk.
var syncCall = regexp.MustCompile(`\.Sync\(\)|syscall\.Fsync\(|unix\.Fsync\(`)

// No fsync on the layout path (ini-5xcb item 4): the shared writer and the
// layout code call none, so a layout save's timing is unchanged.
func TestLayoutPath_CallsNoFsync(t *testing.T) {
	for _, file := range []string{"atomic_write.go", "layout.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if found := syncCall.FindString(string(src)); found != "" {
			t.Errorf("%s calls %q on the layout path", file, found)
		}
	}
}

// The twin: the guard finds a planted sync of each kind.
func TestFsyncGuard_FindsAPlantedSync(t *testing.T) {
	for _, planted := range []string{"if err := f.Sync(); err != nil {", "syscall.Fsync(fd)", "unix.Fsync(fd)"} {
		if !syncCall.MatchString(planted) {
			t.Errorf("the guard missed %q", planted)
		}
	}
}
