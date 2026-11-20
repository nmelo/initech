package tui

import "github.com/gdamore/tcell/v2"

// windowPalette is the colours initech's windows draw with: the agents
// window, help, top and the watches window. One definition, so the windows
// read as one program. The first roles are the values help, top and the
// agents window have always used; the derived roles are steps from the panel
// colour for what only a table with bands needs, and the backdrop is what
// darkens the screen behind a window.
type windowPalette struct {
	// Panel is a window's ground.
	Panel tcell.Color
	// Text is ordinary text.
	Text tcell.Color
	// Border is the frame and the rule above a key line.
	Border tcell.Color
	// Title is a window's name on its frame, drawn bold.
	Title tcell.Color
	// Heading is a section or group heading, drawn bold.
	Heading tcell.Color
	// ColumnHead is a table's column names, drawn bold.
	ColumnHead tcell.Color
	// Hint is a key line's words and a title's separators.
	Hint tcell.Color
	// Dim is text that has gone quiet: an ended row, an age.
	Dim tcell.Color
	// SelectionBg and SelectionFg are the selected row.
	SelectionBg, SelectionFg tcell.Color
	// Accent marks a name or id to be told apart: a suspended agent, a bead id.
	Accent tcell.Color
	// OK, Warning and Bad are the state roles: done, waiting or held, failed.
	OK, Warning, Bad tcell.Color

	// Derived, for a table with bands (the watches window):
	// ColumnShade is every other column's ground, one faint step up from
	// Panel; HeaderBand is the column names' ground, a step down from Panel;
	// GroupBand is a group label's ground, two steps up from Panel.
	ColumnShade, HeaderBand, GroupBand tcell.Color

	// Backdrop is the colour laid over the screen behind a window, and
	// BackdropOpacity how much of it, in percent.
	Backdrop        tcell.Color
	BackdropOpacity int32
}

// palette is the one definition.
var palette = windowPalette{
	Panel:       tcell.NewRGBColor(20, 20, 20),
	Text:        tcell.ColorSilver,
	Border:      tcell.ColorGray,
	Title:       tcell.ColorDodgerBlue,
	Heading:     tcell.ColorYellow,
	ColumnHead:  tcell.ColorWhite,
	Hint:        tcell.ColorGray,
	Dim:         tcell.ColorGray,
	SelectionBg: tcell.ColorDarkBlue,
	SelectionFg: tcell.ColorWhite,
	Accent:      tcell.ColorDodgerBlue,
	OK:          tcell.ColorGreen,
	Warning:     tcell.ColorYellow,
	Bad:         tcell.ColorRed,

	ColumnShade: tcell.NewRGBColor(27, 27, 27),
	HeaderBand:  tcell.NewRGBColor(10, 10, 10),
	GroupBand:   tcell.NewRGBColor(44, 44, 44),

	Backdrop:        tcell.NewRGBColor(6, 7, 9),
	BackdropOpacity: 86,
}

// UnderBackdrop is c seen through the backdrop. A colour with no RGB value
// (the terminal's default) is taken as fallback first.
func (p windowPalette) UnderBackdrop(c, fallback tcell.Color) tcell.Color {
	r, g, b := c.RGB()
	if r < 0 {
		r, g, b = fallback.RGB()
	}
	br, bg, bb := p.Backdrop.RGB()
	o := p.BackdropOpacity
	mix := func(v, back int32) int32 { return (v*(100-o) + back*o) / 100 }
	return tcell.NewRGBColor(mix(r, br), mix(g, bg), mix(b, bb))
}
