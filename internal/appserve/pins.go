package appserve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PinsFile is the pinned list, under the service's state folder, so every
// paired device shows the same list.
const PinsFile = "pins.json"

// Limits on a pinned list a PUT may store.
const (
	MaxPins       = 200
	MaxPinName    = 80
	maxPinsBodyKB = 64
)

// Pin is one pinned pane: which rig and pane, the name Nelson gave it, and
// its place in his order.
type Pin struct {
	Rig   string `json:"rig"`
	Pane  string `json:"pane"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}

// LoadPins is the stored pinned list in order, empty when none is stored.
func LoadPins(dir string) ([]Pin, error) {
	raw, err := os.ReadFile(filepath.Join(dir, PinsFile))
	if errors.Is(err, os.ErrNotExist) {
		return []Pin{}, nil
	}
	if err != nil {
		return nil, err
	}
	var pins []Pin
	if err := json.Unmarshal(raw, &pins); err != nil {
		return nil, fmt.Errorf("%s: %w", PinsFile, err)
	}
	if pins == nil {
		pins = []Pin{}
	}
	sortPins(pins)
	return pins, nil
}

// SavePins validates pins and replaces the stored list with them.
func SavePins(dir string, pins []Pin) error {
	if err := validatePins(pins); err != nil {
		return err
	}
	sortPins(pins)
	data, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writePrivate(filepath.Join(dir, PinsFile), append(data, '\n'))
}

func sortPins(pins []Pin) {
	sort.SliceStable(pins, func(i, j int) bool { return pins[i].Order < pins[j].Order })
}

// validatePins refuses a list with a pin missing its rig or pane, a name
// over MaxPinName characters, the same pane twice, or over MaxPins pins.
func validatePins(pins []Pin) error {
	if len(pins) > MaxPins {
		return fmt.Errorf("%d pins; at most %d", len(pins), MaxPins)
	}
	seen := map[[2]string]bool{}
	for i, p := range pins {
		if strings.TrimSpace(p.Rig) == "" || strings.TrimSpace(p.Pane) == "" {
			return fmt.Errorf("pin %d: rig and pane are required", i)
		}
		if n := len([]rune(p.Name)); n > MaxPinName {
			return fmt.Errorf("pin %d: name is %d characters; at most %d", i, n, MaxPinName)
		}
		k := [2]string{p.Rig, p.Pane}
		if seen[k] {
			return fmt.Errorf("pin %d: %s/%s is pinned twice", i, p.Rig, p.Pane)
		}
		seen[k] = true
	}
	return nil
}

func (s *Server) handleGetPins(w http.ResponseWriter, r *http.Request) {
	pins, err := LoadPins(s.cfg.StateDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, pins)
}

func (s *Server) handlePutPins(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPinsBodyKB<<10+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(body) > maxPinsBodyKB<<10 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("pinned list over %d KB", maxPinsBodyKB)})
		return
	}
	var pins []Pin
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pins); err != nil || pins == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body is not a list of pins"})
		return
	}
	s.pinsMu.Lock()
	defer s.pinsMu.Unlock()
	if err := SavePins(s.cfg.StateDir, pins); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, pins)
}
