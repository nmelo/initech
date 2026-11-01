package appserve

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"rsc.io/qr"
)

// ini-r0q.12: the pairing QR. The rendered text is parsed back to modules and
// compared with rsc.io/qr's own encoding of the URL; zbarimg decodes a real
// render by hand (recorded on the bead).

const qrTestURL = "initech://pair?host=100.101.1.2&port=7392&token=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&fp=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// qrModules parses a render back to its rows of modules, half-block by
// half-block, checking each row's colours.
func qrModules(t *testing.T, render string) [][]bool {
	t.Helper()
	if !strings.HasSuffix(render, "\n") {
		t.Fatal("the render does not end with a newline")
	}
	var out [][]bool
	for _, line := range strings.Split(strings.TrimSuffix(render, "\n"), "\n") {
		if !strings.HasPrefix(line, qrLineStart) || !strings.HasSuffix(line, qrLineEnd) {
			t.Fatalf("a row is not black on white: %q", line)
		}
		cells := strings.TrimSuffix(strings.TrimPrefix(line, qrLineStart), qrLineEnd)
		var top, bottom []bool
		for _, r := range cells {
			switch r {
			case '█':
				top, bottom = append(top, true), append(bottom, true)
			case '▀':
				top, bottom = append(top, true), append(bottom, false)
			case '▄':
				top, bottom = append(top, false), append(bottom, true)
			case ' ':
				top, bottom = append(top, false), append(bottom, false)
			default:
				t.Fatalf("a row holds %q", r)
			}
		}
		out = append(out, top, bottom)
	}
	return out
}

// AC4: the render is exactly rsc.io/qr's encoding of the URL, inside a full
// quiet zone.
func TestRenderQR_IsTheEncodingOfTheURL(t *testing.T) {
	for _, url := range []string{qrTestURL, "x", "initech://pair?host=192.168.1.5&port=20000&token=t&fp=f"} {
		render, err := RenderQR(url)
		if err != nil {
			t.Fatalf("%q: %v", url, err)
		}
		code, err := qr.Encode(url, qr.L)
		if err != nil {
			t.Fatal(err)
		}
		mods := qrModules(t, render)
		side := code.Size + 2*qrQuietZone
		if len(mods) < side || len(mods) > side+1 {
			t.Fatalf("%q: %d module rows, want %d", url, len(mods), side)
		}
		for y, row := range mods {
			if len(row) != side {
				t.Fatalf("%q: row %d is %d modules wide, want %d", url, y, len(row), side)
			}
			for x, got := range row {
				want := code.Black(x-qrQuietZone, y-qrQuietZone)
				if got != want {
					t.Fatalf("%q: module (%d,%d) is %v, want %v", url, x, y, got, want)
				}
			}
		}
		for i := 0; i < side; i++ {
			for q := 0; q < qrQuietZone; q++ {
				if mods[q][i] || mods[i][q] || mods[side-1-q][i] || mods[i][side-1-q] {
					t.Fatalf("%q: a black module in the quiet zone near (%d,%d)", url, i, q)
				}
			}
		}
	}
}

// The render of one URL is not the render of another.
func TestRenderQR_ADifferentURLIsADifferentCode(t *testing.T) {
	a, errA := RenderQR(qrTestURL)
	b, errB := RenderQR(qrTestURL + "x")
	if errA != nil || errB != nil || a == b {
		t.Errorf("errors %v, %v; same render: %v", errA, errB, a == b)
	}
}

// A pairing URL's width, as the docs state it: about 57 columns.
func TestRenderQR_APairingURLFitsItsStatedWidth(t *testing.T) {
	render, err := RenderQR(qrTestURL)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(render, "\n", 2)[0]
	cells := strings.TrimSuffix(strings.TrimPrefix(first, qrLineStart), qrLineEnd)
	if w := utf8.RuneCountInString(cells); w > 57 {
		t.Errorf("a pairing URL renders %d columns wide; the docs say about 57", w)
	}
}

// AC5: an empty URL gives no QR and a named error.
func TestRenderQR_AnEmptyURLIsANamedError(t *testing.T) {
	render, err := RenderQR("")
	if !errors.Is(err, ErrNoPairURL) || render != "" {
		t.Errorf("render %q, err %v", render, err)
	}
}

// The quiet zone and the colours are the ruled ones, literally: four modules,
// black on white.
func TestRenderQR_TheQuietZoneAndColoursAreTheRuledOnes(t *testing.T) {
	if qrQuietZone != 4 {
		t.Errorf("quiet zone %d, want 4", qrQuietZone)
	}
	if qrLineStart != "\x1b[30;107m" || qrLineEnd != "\x1b[0m" {
		t.Errorf("colours %q %q, want black on white and a reset", qrLineStart, qrLineEnd)
	}
}
