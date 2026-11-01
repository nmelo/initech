package appserve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Typed is what POST /v1/keys and /v1/send answer: the verdict word the TUI
// gave (delivered, held, deferred, unchecked, unconfirmed, not_delivered; or
// refused when the service turned the request away itself) and the words
// the matching command prints for the same call.
type Typed struct {
	Verdict string `json:"verdict"`
	Words   string `json:"words"`
}

// VerdictRefused is the verdict of a request the service turns away before
// anything is typed: a bad body, an unknown key, rig or pane, or a backend
// that cannot type.
const VerdictRefused = "refused"

// KeyTypist is a backend that can press one key in a pane, as initech
// keystroke does, as the operator. cmd implements it over the command's own
// code, so the verdict and words are the command's, never rebuilt here.
type KeyTypist interface {
	// KeyRefusal is the command's refusal line for a key outside its table,
	// and false; "" and true for a key it takes. It makes no call.
	KeyRefusal(pane, key string) (string, bool)
	// Key presses key in pane of rig r and answers the command's verdict
	// and words.
	Key(r Rig, pane, key string) Typed
}

// maxKeyBody bounds a keys request body: one short JSON object.
const maxKeyBody = 4 << 10

// handleKeys is POST /v1/keys/{rig}/{pane} with body {"key": one of the
// five}: 200 with the command's verdict and words once a key was tried; a
// refusal (400 bad body or key, 404 unknown rig or pane, 501 a backend that
// cannot type) answers {verdict: refused, words} and types nothing.
func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	rigName, pane := r.PathValue("rig"), r.PathValue("pane")
	kt, ok := s.cfg.Backend.(KeyTypist)
	if !ok {
		refuse(w, http.StatusNotImplemented, "this initech app-serve cannot type into panes")
		return
	}
	var body struct {
		Key *string `json:"key"`
	}
	if words := readBody(r, w, maxKeyBody, &body); words != "" {
		refuse(w, http.StatusBadRequest, words)
		return
	}
	key := ""
	if body.Key != nil {
		key = *body.Key
	}
	if words, ok := kt.KeyRefusal(pane, key); !ok {
		refuse(w, http.StatusBadRequest, words)
		return
	}
	rg, words := s.typingTarget(rigName, pane)
	if words != "" {
		refuse(w, http.StatusNotFound, words)
		return
	}
	writeJSON(w, http.StatusOK, kt.Key(rg, pane, key))
}

// typingTarget is the running rig named rigName, checked to have pane, or
// the words of why not. It reads the pane list; it types nothing.
func (s *Server) typingTarget(rigName, pane string) (Rig, string) {
	for _, rg := range s.running() {
		if rg.Name != rigName {
			continue
		}
		panes, err := s.cfg.Backend.Panes(rg)
		if err != nil {
			return Rig{}, fmt.Sprintf("no running rig %q", rigName)
		}
		for _, p := range panes {
			if p.Name == pane {
				return rg, ""
			}
		}
		return Rig{}, fmt.Sprintf("pane %q not found in rig %q", pane, rigName)
	}
	return Rig{}, fmt.Sprintf("no running rig %q", rigName)
}

// readBody decodes one JSON object of at most limit bytes into v, or answers
// the words of why it could not.
func readBody(r *http.Request, w http.ResponseWriter, limit int64, v any) string {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return fmt.Sprintf("the body is over %d bytes", limit)
		}
		return "the body could not be read"
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "the body is empty; it must be a JSON object"
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return "the body is not a JSON object: " + err.Error()
	}
	return ""
}

// refuse answers a request turned away before anything was typed.
func refuse(w http.ResponseWriter, code int, words string) {
	writeJSON(w, code, Typed{Verdict: VerdictRefused, Words: words})
}

// MessageTypist is a backend that can send a message into a pane, as
// initech send does, as the operator. cmd implements it over the command's
// own code, so the verdict and words are the command's, never rebuilt here.
type MessageTypist interface {
	// Send types text into pane of rig r (one Enter after it when enter)
	// and answers the command's verdict and words: delivered, deferred (a
	// dialog is open; the text is kept), held (do-not-disturb; kept),
	// unchecked, unconfirmed or not_delivered.
	Send(r Rig, pane, text string, enter bool) Typed
}

// maxSendBody bounds a send request body.
const maxSendBody = 1 << 20

// handleSend is POST /v1/send/{rig}/{pane} with body {"text": "...",
// "enter": true}: enter absent means true (one Enter, as initech send);
// false is initech send --no-enter. 200 with the command's verdict and
// words once the text was offered to the pane; a refusal (400 bad body or
// empty text, 404 unknown rig or pane, 501 a backend that cannot send)
// answers {verdict: refused, words} and sends nothing.
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	rigName, pane := r.PathValue("rig"), r.PathValue("pane")
	mt, ok := s.cfg.Backend.(MessageTypist)
	if !ok {
		refuse(w, http.StatusNotImplemented, "this initech app-serve cannot send into panes")
		return
	}
	var body struct {
		Text  *string `json:"text"`
		Enter *bool   `json:"enter"`
	}
	if words := readBody(r, w, maxSendBody, &body); words != "" {
		refuse(w, http.StatusBadRequest, words)
		return
	}
	if body.Text == nil || *body.Text == "" {
		refuse(w, http.StatusBadRequest, "nothing to send: the text is empty")
		return
	}
	enter := body.Enter == nil || *body.Enter
	rg, words := s.typingTarget(rigName, pane)
	if words != "" {
		refuse(w, http.StatusNotFound, words)
		return
	}
	writeJSON(w, http.StatusOK, mt.Send(rg, pane, *body.Text, enter))
}
