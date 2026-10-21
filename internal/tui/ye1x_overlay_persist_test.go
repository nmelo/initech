package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"gopkg.in/yaml.v3"
)

// ini-ye1x: the Agents overlay (Alt+s, the panel command) comes back after a
// relaunch shown or hidden as it was left. layout.yaml carries one optional
// key, overlay, written on every save; a file without it loads shown.

var ye1xNames = []string{"super", "eng1", "eng2", "qa1"}

func ye1xSaved(t *testing.T, overlay bool) (string, LayoutState) {
	t.Helper()
	root := t.TempDir()
	st := DefaultLayoutState(ye1xNames)
	st.Overlay = overlay
	if err := SaveLayout(root, st); err != nil {
		t.Fatal(err)
	}
	ls, ok := LoadLayout(root, ye1xNames)
	if !ok {
		t.Fatal("the saved layout did not load")
	}
	return root, ls
}

func ye1xFile(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(layoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Criterion 1: hidden comes back hidden; shown comes back shown.
func TestOverlayPersist_ComesBackAsItWasLeft(t *testing.T) {
	if _, ls := ye1xSaved(t, false); ls.Overlay {
		t.Error("the overlay was hidden at save and came back shown")
	}
	if _, ls := ye1xSaved(t, true); !ls.Overlay {
		t.Error("the overlay was shown at save and came back hidden")
	}
}

// Every save writes the key, true or false.
func TestOverlayPersist_EverySaveWritesTheKey(t *testing.T) {
	for _, overlay := range []bool{true, false} {
		root, _ := ye1xSaved(t, overlay)
		want := "overlay: false"
		if overlay {
			want = "overlay: true"
		}
		if got := ye1xFile(t, root); !strings.Contains(got, want) {
			t.Errorf("saved with overlay %v, the file lacks %q:\n%s", overlay, want, got)
		}
	}
}

// Criterion 2: a file v2.31.4 wrote (no overlay key; SaveLayout at the
// v2.31.4 tag, b8fca8a) loads with the overlay shown.
func TestOverlayPersist_AV2314FileLoadsShown(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "layout_v2314.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "overlay") {
		t.Fatal("the v2.31.4 fixture carries an overlay key; it must be the old format")
	}
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	if err := os.WriteFile(layoutPath(root), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ls, ok := LoadLayout(root, ye1xNames)
	if !ok || !ls.Overlay {
		t.Errorf("a v2.31.4 file loaded ok=%v overlay=%v, want the overlay shown", ok, ls.Overlay)
	}
	if ls.GridCols != 2 || ls.GridRows != 2 || !ls.GridExplicit || ls.Mode != LayoutGrid {
		t.Errorf("the rest of the v2.31.4 file changed: %+v", ls)
	}
}

// persistentLayoutV2314 is PersistentLayout as v2.31.4 declares it (b8fca8a):
// an older initech reading a newer file decodes into this.
type persistentLayoutV2314 struct {
	Grid         string            `yaml:"grid"`
	GridExplicit bool              `yaml:"grid_explicit,omitempty"`
	Hidden       []string          `yaml:"hidden,omitempty"`
	Protected    []string          `yaml:"protected,omitempty"`
	DepPinned    []string          `yaml:"pinned,omitempty"`
	Order        []string          `yaml:"order,omitempty"`
	Mode         string            `yaml:"mode"`
	LivePinned   map[string]int    `yaml:"live_pinned,omitempty"`
	LiveAuto     bool              `yaml:"live_auto,omitempty"`
	Groups       []string          `yaml:"groups,omitempty"`
	GroupOf      map[string]string `yaml:"group_of,omitempty"`
	Split        string            `yaml:"split,omitempty"`
	Held         string            `yaml:"held,omitempty"`
	ListRows     int               `yaml:"list_rows,omitempty"`
}

// Criterion 3: a v2.31.4 reader decodes a file with overlay: false.
func TestOverlayPersist_AnOlderReaderIgnoresTheKey(t *testing.T) {
	root, _ := ye1xSaved(t, false)
	var old persistentLayoutV2314
	if err := yaml.Unmarshal([]byte(ye1xFile(t, root)), &old); err != nil {
		t.Fatalf("a v2.31.4 reader failed on the new file: %v", err)
	}
	if old.Mode != "grid" || old.Grid == "" {
		t.Errorf("a v2.31.4 reader read %+v", old)
	}
}

// ye1xWindow is window 1 over a temp project, overlay shown.
func ye1xWindow(t *testing.T) *TUI {
	t.Helper()
	tui := listFleet(t, ye1xNames...)
	tui.projectRoot = t.TempDir()
	tui.layoutState.Overlay = true
	return tui
}

// Criterion 4: the key and the command each write the file at once.
func TestOverlayPersist_BothTogglesSave(t *testing.T) {
	for name, toggle := range map[string]func(*TUI){
		"Alt+s":   func(tui *TUI) { tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt)) },
		"command": func(tui *TUI) { tui.execCmd("panel") },
	} {
		tui := ye1xWindow(t)
		toggle(tui)
		if tui.layoutState.Overlay {
			t.Fatalf("%s did not hide the overlay", name)
		}
		ls, ok := LoadLayout(tui.projectRoot, ye1xNames)
		if !ok || ls.Overlay {
			t.Errorf("%s: the file says ok=%v overlay=%v, want it saved hidden", name, ok, ls.Overlay)
		}
		toggle(tui)
		if ls, _ := LoadLayout(tui.projectRoot, ye1xNames); !ls.Overlay {
			t.Errorf("%s again: the file still says hidden", name)
		}
	}
}

// Criterion 5: a second window starts shown whatever window 1 saved, and its
// toggles write nothing.
func TestOverlayPersist_ASecondWindowStartsShownAndWritesNothing(t *testing.T) {
	root, ls := ye1xSaved(t, false)
	before := ye1xFile(t, root)
	viewer := listFleet(t, ye1xNames...)
	viewer.projectRoot = root
	viewer.windowID = "window-2"
	viewer.layoutState = ls
	viewer.adoptLoadedOverlay(true)
	if !viewer.layoutState.Overlay {
		t.Error("a second window started with the overlay hidden")
	}
	viewer.execCmd("panel")
	viewer.handleKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt))
	if after := ye1xFile(t, root); after != before {
		t.Errorf("a second window rewrote layout.yaml:\n%s", after)
	}
	// Window 1 adopting the same file keeps it hidden.
	w1 := listFleet(t, ye1xNames...)
	w1.layoutState = ls
	w1.adoptLoadedOverlay(false)
	if w1.layoutState.Overlay {
		t.Error("window 1 lost the saved hidden overlay on adopt")
	}
}

// Criterion 6: a reset launch starts from the defaults, overlay shown.
func TestOverlayPersist_AResetStartsShown(t *testing.T) {
	root, _ := ye1xSaved(t, false)
	DeleteLayout(root)
	if _, ok := LoadLayout(root, ye1xNames); ok {
		t.Fatal("the layout survived the reset")
	}
	if !DefaultLayoutState(ye1xNames).Overlay {
		t.Error("the default layout has the overlay hidden")
	}
}

// Feed it nothing: an empty file and a file with only the overlay key.
func TestOverlayPersist_EmptyAndOverlayOnlyFiles(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(layoutDir(root), 0o700)
	os.WriteFile(layoutPath(root), nil, 0o600)
	if _, ok := LoadLayout(root, ye1xNames); ok {
		t.Error("an empty file loaded as a layout")
	}
	os.WriteFile(layoutPath(root), []byte("overlay: false\n"), 0o600)
	if ls, ok := LoadLayout(root, ye1xNames); ok && ls.Overlay {
		t.Error("a file with only overlay: false loaded with the overlay shown")
	}
}
