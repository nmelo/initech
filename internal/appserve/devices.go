package appserve

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DevicesFile is the registered devices, under the service's state folder.
const DevicesFile = "devices.json"

// Limits on the registered devices.
const (
	MaxDevices        = 10
	MaxDeviceTokenLen = 200
	maxDeviceBody     = 4 << 10
)

// Device is one phone or iPad the service pushes to: its APNs device token,
// lowercase hex, and when it registered.
type Device struct {
	Token string    `json:"token"`
	Added time.Time `json:"added"`
}

// devicesDoc is the file's shape: {"devices": [...]}.
type devicesDoc struct {
	Devices []Device `json:"devices"`
}

// Devices is the stored device set. Every change rewrites the file (0600)
// before it is answered, so the set survives a restart.
type Devices struct {
	dir string
	now func() time.Time

	mu   sync.Mutex
	list []Device
}

// errRefused is a change the set turns down, with the words for the caller.
type errRefused struct{ words string }

func (e *errRefused) Error() string { return e.words }

// errUnknownDevice is a DELETE of a token that is not registered.
var errUnknownDevice = errors.New("no such device")

// LoadDevices is the set stored in dir; none stored is an empty set.
func LoadDevices(dir string, now func() time.Time) (*Devices, error) {
	d := &Devices{dir: dir, now: now}
	raw, err := os.ReadFile(filepath.Join(dir, DevicesFile))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	var doc devicesDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", DevicesFile, err)
	}
	d.list = doc.Devices
	return d, nil
}

// Tokens is every registered token, in the order they registered.
func (d *Devices) Tokens() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.list))
	for i, dv := range d.list {
		out[i] = dv.Token
	}
	return out
}

// normalToken is token as it is stored, or the words refusing it: hex only,
// at most MaxDeviceTokenLen characters.
func normalToken(token string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(token))
	switch {
	case t == "":
		return "", &errRefused{"token is required"}
	case len(t) > MaxDeviceTokenLen:
		return "", &errRefused{fmt.Sprintf("token is %d characters; at most %d", len(t), MaxDeviceTokenLen)}
	}
	if _, err := hex.DecodeString(t); err != nil {
		return "", &errRefused{"token is not hex"}
	}
	return t, nil
}

// Add registers token. A token already registered changes nothing; an
// eleventh is refused. It answers the count.
func (d *Devices) Add(token string) (int, error) {
	t, err := normalToken(token)
	if err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, dv := range d.list {
		if dv.Token == t {
			return len(d.list), nil
		}
	}
	if len(d.list) >= MaxDevices {
		return 0, &errRefused{fmt.Sprintf("%d devices are registered, the most there can be; remove one first", MaxDevices)}
	}
	next := append(append([]Device(nil), d.list...), Device{Token: t, Added: d.now().UTC()})
	if err := d.save(next); err != nil {
		return 0, err
	}
	d.list = next
	return len(d.list), nil
}

// Remove unregisters token. It answers the count, or errUnknownDevice.
func (d *Devices) Remove(token string) (int, error) {
	t, err := normalToken(token)
	if err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	next := make([]Device, 0, len(d.list))
	for _, dv := range d.list {
		if dv.Token != t {
			next = append(next, dv)
		}
	}
	if len(next) == len(d.list) {
		return 0, errUnknownDevice
	}
	if err := d.save(next); err != nil {
		return 0, err
	}
	d.list = next
	return len(d.list), nil
}

// save writes list as the file. d.mu is held.
func (d *Devices) save(list []Device) error {
	if list == nil {
		list = []Device{}
	}
	data, err := json.MarshalIndent(devicesDoc{Devices: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	return writePrivate(filepath.Join(d.dir, DevicesFile), append(data, '\n'))
}

// deviceAnswer writes a change's result: the count, or the refusal.
func deviceAnswer(w http.ResponseWriter, n int, err error) {
	var r *errRefused
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]int{"devices": n})
	case errors.As(err, &r):
		refuse(w, http.StatusBadRequest, r.words)
	case errors.Is(err, errUnknownDevice):
		refuse(w, http.StatusNotFound, "that device is not registered")
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// handlePutDevice is PUT /v1/devices {"token": "<hex>"}: register, once.
func (s *Server) handlePutDevice(w http.ResponseWriter, r *http.Request) {
	if s.devices == nil {
		refuse(w, http.StatusServiceUnavailable, "devices are not stored here")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDeviceBody+1))
	if err != nil || len(body) > maxDeviceBody {
		refuse(w, http.StatusBadRequest, "body is not one device")
		return
	}
	var req struct {
		Token *string `json:"token"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Token == nil {
		refuse(w, http.StatusBadRequest, `body is not {"token": "<hex APNs device token>"}`)
		return
	}
	n, err := s.devices.Add(*req.Token)
	deviceAnswer(w, n, err)
}

// handleDeleteDevice is DELETE /v1/devices/{token}: unregister.
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if s.devices == nil {
		refuse(w, http.StatusServiceUnavailable, "devices are not stored here")
		return
	}
	n, err := s.devices.Remove(r.PathValue("token"))
	deviceAnswer(w, n, err)
}
