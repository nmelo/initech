package tui

import (
	"os"
	"regexp"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// colourLiteral is a colour named in source: an RGB or hex constructor, or a
// named tcell colour (tcell.ColorRed, not the type tcell.Color).
var colourLiteral = regexp.MustCompile(`tcell\.NewRGBColor\(|tcell\.NewHexColor\(|tcell\.GetColor\(|tcell\.Color[A-Z][A-Za-z0-9]*`)

// The watches window names no colour of its own: every one comes from the
// palette (ini-yicd.1).
func TestWatchesModal_NamesNoColourOutsideThePalette(t *testing.T) {
	src, err := os.ReadFile("watches_modal.go")
	if err != nil {
		t.Fatal(err)
	}
	if found := colourLiteral.FindAllString(string(src), -1); len(found) > 0 {
		t.Errorf("watches_modal.go names %d colours outside the palette, the first %q", len(found), found[0])
	}
}

// The twin: the guard finds a planted literal of each kind, so its silence on
// the real file means something.
func TestColourGuard_FindsAPlantedLiteral(t *testing.T) {
	for _, planted := range []string{
		`bg := tcell.NewRGBColor(1, 2, 3)`,
		`bg := tcell.NewHexColor(0x123456)`,
		`fg := tcell.ColorRed`,
		`fg := tcell.GetColor("red")`,
	} {
		if !colourLiteral.MatchString(planted) {
			t.Errorf("the guard missed %q", planted)
		}
	}
	if colourLiteral.MatchString(`func dim(c tcell.Color) tcell.Color { return c }`) {
		t.Error("the guard took the type tcell.Color for a colour")
	}
}

// The palette's first roles are the values help, top and the agents window
// have always used, written out here so a changed value is a failure, not a
// silent restyle of every window.
func TestPalette_IsTheSiblingWindowsColours(t *testing.T) {
	for _, c := range []struct {
		role      string
		got, want tcell.Color
	}{
		{"panel", palette.Panel, tcell.NewRGBColor(20, 20, 20)},
		{"text", palette.Text, tcell.ColorSilver},
		{"border", palette.Border, tcell.ColorGray},
		{"title", palette.Title, tcell.ColorDodgerBlue},
		{"heading", palette.Heading, tcell.ColorYellow},
		{"column head", palette.ColumnHead, tcell.ColorWhite},
		{"hint", palette.Hint, tcell.ColorGray},
		{"dim", palette.Dim, tcell.ColorGray},
		{"selection", palette.SelectionBg, tcell.ColorDarkBlue},
		{"selection text", palette.SelectionFg, tcell.ColorWhite},
		{"accent", palette.Accent, tcell.ColorDodgerBlue},
		{"ok", palette.OK, tcell.ColorGreen},
		{"warning", palette.Warning, tcell.ColorYellow},
		{"bad", palette.Bad, tcell.ColorRed},
	} {
		if c.got != c.want {
			t.Errorf("%s is %v, want %v", c.role, c.got, c.want)
		}
	}
}
