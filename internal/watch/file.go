package watch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// FileVersion is the watches file's format version. A file of another version
// is set aside, never half-read.
const FileVersion = 1

// fileBody is the whole watches file.
type fileBody struct {
	Version int     `yaml:"version"`
	NextID  int     `yaml:"next_id"`
	Watches []Watch `yaml:"watches"`
}

var idPattern = regexp.MustCompile(`^w([1-9][0-9]*)$`)

// Open reads the watches file at path into a store loaded at now. A missing or
// empty file is an empty store. A file that does not parse, has another
// version, or holds a watch with no id, a duplicate id, no owner, no kind or a
// state outside the four, is renamed <path>.bad-<time>, never deleted, and the
// store starts empty; note says so, for the caller's log line and its line to
// super. A watch of a kind this build does not know is kept as it is.
func Open(path string, now time.Time) (*Store, string) {
	s := &Store{path: path, loaded: now, nextID: 1, write: writeAtomic, pending: map[string]bool{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, ""
	}
	if err == nil && len(bytes.TrimSpace(data)) == 0 {
		return s, ""
	}
	if err == nil {
		var ws []*Watch
		var next int
		if ws, next, err = parse(data); err == nil {
			s.ws, s.nextID = ws, next
			return s, ""
		}
	}
	aside := path + ".bad-" + now.UTC().Format("20060102T150405Z")
	if rerr := os.Rename(path, aside); rerr != nil {
		s.broken = fmt.Errorf("%s could not be read (%v) nor set aside (%v)", filepath.Base(path), err, rerr)
		return s, s.broken.Error() + "; no watch can be set until it is fixed"
	}
	return s, fmt.Sprintf("%s could not be read (%v): set aside as %s; starting with no watches", filepath.Base(path), err, filepath.Base(aside))
}

// parse checks a file's every watch before any is used: all or nothing.
func parse(data []byte) ([]*Watch, int, error) {
	var body fileBody
	if err := yaml.Unmarshal(data, &body); err != nil {
		return nil, 0, fmt.Errorf("it does not parse: %v", err)
	}
	if body.Version != FileVersion {
		return nil, 0, fmt.Errorf("version %d, want %d", body.Version, FileVersion)
	}
	seen := map[string]bool{}
	next := max(body.NextID, 1)
	var ws []*Watch
	for i := range body.Watches {
		w := body.Watches[i]
		m := idPattern.FindStringSubmatch(w.ID)
		switch {
		case m == nil:
			return nil, 0, fmt.Errorf("watch %d has no id like w12 (%q)", i+1, w.ID)
		case seen[w.ID]:
			return nil, 0, fmt.Errorf("id %s appears twice", w.ID)
		case w.Owner == "":
			return nil, 0, fmt.Errorf("%s has no owner", w.ID)
		case w.Kind == "":
			return nil, 0, fmt.Errorf("%s has no condition kind", w.ID)
		case w.State != Waiting && w.State != Fired && w.State != Missed && w.State != Cancelled:
			return nil, 0, fmt.Errorf("%s has state %q", w.ID, w.State)
		}
		seen[w.ID] = true
		if n, _ := strconv.Atoi(m[1]); n >= next {
			next = n + 1 // ids are never reused, whatever next_id says
		}
		ws = append(ws, &w)
	}
	return ws, next, nil
}

func (s *Store) marshal() ([]byte, error) {
	body := fileBody{Version: FileVersion, NextID: s.nextID, Watches: make([]Watch, 0, len(s.ws))}
	for _, w := range s.ws {
		body.Watches = append(body.Watches, *w)
	}
	return yaml.Marshal(body)
}

// writeAtomic writes data to path whole: a temporary file beside it, mode
// 0600, synced, then renamed over the old one, so a crash leaves the old file
// or the new one and never half of either.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".watches-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	ok = true
	return os.Rename(name, path)
}
