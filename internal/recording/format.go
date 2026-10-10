// Package recording reads and writes initech PTY recordings: the exact bytes
// each agent pane's child printed, with their timing and the pane's size
// changes, one file per pane (ini-pqdy.2). The efficiency sprint's lab replays
// these into shell-backed panes (ini-pqdy.3) so N panes carry real fleet
// output without N real agents.
//
// Wire format (agreed between the writer, ini-pqdy.2, and the replayer,
// ini-pqdy.3):
//
//	"IREC" magic (4 bytes), then records back to back:
//	kind (1 byte) | t (uint64 BE, ns since start, monotonic) | len (uint32 BE) | payload
//
// Kinds: 'H' header JSON (first record, t=0), 'O' output bytes (one PTY read),
// 'R' resize JSON {"cols","rows"}, 'T' trailer JSON {"dropped"} on a clean
// close. A reader skips kinds it does not know and treats a record cut off at
// end of file as the end of the recording, so a crashed session still reads.
// Input to the child is never recorded.
package recording

import "time"

// Magic opens every recording file.
const Magic = "IREC"

// Version is the header version this package writes and the highest it reads.
const Version = 1

// Record kinds. KindHeader is consumed by NewReader and never returned by Next.
const (
	KindHeader  byte = 'H'
	KindOutput  byte = 'O'
	KindResize  byte = 'R'
	KindTrailer byte = 'T'
)

// FileExt is the extension recording files carry.
const FileExt = ".irec"

// recordHeaderLen is kind + t + len.
const recordHeaderLen = 1 + 8 + 4

// maxPayload bounds a single record so a corrupt length cannot make a reader
// allocate gigabytes. A PTY read is at most 32 KiB in initech; 16 MiB is far
// beyond any real record.
const maxPayload = 16 << 20

// Header is the first record of a recording.
type Header struct {
	Version   int       `json:"version"`
	Pane      string    `json:"pane"`
	Start     time.Time `json:"start"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	ShapeOnly bool      `json:"shape_only,omitempty"`
}

// Record is one record after the header.
type Record struct {
	Kind    byte          // KindOutput, KindResize or KindTrailer.
	At      time.Duration // Time since the recording started.
	Data    []byte        // Output bytes (KindOutput only).
	Cols    int           // New size (KindResize only).
	Rows    int           // New size (KindResize only).
	Dropped uint64        // Chunks the recorder dropped (KindTrailer only).
}

type resizeBody struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type trailerBody struct {
	Dropped uint64 `json:"dropped"`
}
