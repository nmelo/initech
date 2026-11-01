package appserve

import (
	"errors"
	"strings"

	"rsc.io/qr"
)

// qrQuietZone is the white border a QR needs around it, in modules.
const qrQuietZone = 4

// qrLineStart and qrLineEnd colour each row black on white whatever the
// terminal's own colours, so a code scans on a dark or a light terminal.
const (
	qrLineStart = "\x1b[30;107m"
	qrLineEnd   = "\x1b[0m"
)

// ErrNoPairURL is RenderQR's answer to an empty URL: no QR.
var ErrNoPairURL = errors.New("no pairing URL to render as a QR")

// RenderQR is url as a QR code at error-correction level L in half-block
// characters, two modules to a character cell, with its quiet zone. Each row
// ends with a newline. initech app-serve token prints it under the same URL
// as text.
func RenderQR(url string) (string, error) {
	if url == "" {
		return "", ErrNoPairURL
	}
	code, err := qr.Encode(url, qr.L)
	if err != nil {
		return "", err
	}
	black := func(x, y int) bool { return code.Black(x-qrQuietZone, y-qrQuietZone) }
	side := code.Size + 2*qrQuietZone
	var b strings.Builder
	for y := 0; y < side; y += 2 {
		b.WriteString(qrLineStart)
		for x := 0; x < side; x++ {
			top, bottom := black(x, y), black(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString(qrLineEnd)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
