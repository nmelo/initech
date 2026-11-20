package appserve

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// queuedPrompt is a message typed while the agent was mid-turn.
func queuedPrompt(uuid, prompt string) string {
	b, _ := json.Marshal(map[string]any{"type": "attachment", "uuid": uuid, "timestamp": t0.Format(time.RFC3339),
		"attachment": map[string]any{"type": "queued_command", "commandMode": "prompt", "prompt": prompt}})
	return string(b)
}

// senderCorpus is one sender of each shape seen in the running rigs'
// transcripts (ini-ieh9: 430 distinct senders over 14 days in five rigs,
// 346 of them more than one word). Names and paths are swapped where the
// original named a person or a private path; length, spacing and
// punctuation are kept. The number is how many entries had that shape.
var senderCorpus = []struct{ sender, shape string }{
	{"thegal", "one word (84 distinct)"},
	{"eng1", "one word with digits"},
	{"pa", "two letters"},
	{"it-disk-watch", "one word with dashes"},
	{"super of nayutal", "role of rig (super of hover: 4389)"},
	{"super of claude-accounts", "role of a dashed rig"},
	{"nayutal super", "rig then role (123)"},
	{"pa, schedule s13", "comma and a schedule (164)"},
	{"initech, watch w3", "initech's own watch line ([from initech, watch <id>])"},
	{"super, scheduled every 15 min, Nelson 2026-10-22", "commas, digits and a date (69)"},
	{"merge.sh for eng7", "a script for an agent"},
	{"shipper2 tag watch", "three words"},
	{"growth of hover, for thegal", "role of rig, for someone"},
	{"arch->eng1 ack", "an arrow"},
	{"shipper + eng6", "a plus"},
	{"super relay? no", "a question mark"},
	{"eng2 of hover, on Nelson’s word typed in my pane", "a curly apostrophe"},
	{"it, theguy's rig", "a straight apostrophe"},
	{"super of hover, ~/Desktop/Projects/hover; Nelson asked me", "a path and a semicolon"},
	{"theguy, Nelson's chief of staff, ~/Desktop/Projects/theguy; Nelson asked me to reach out", "a whole sentence, 88"},
	{senderOf139, "the longest seen, 139, carrying its reply instructions"},
}

// senderOf139 has the shape and length of the longest sender seen (139
// characters, once in 14 days), with its path and command swapped.
var senderOf139 = func() string {
	s := "super of hover, ~/Desktop/Projects/hover; reply: initech send super with your answer from the rig root"
	return s + strings.Repeat(".", 139-len([]rune(s)))
}()

func senderPage(t *testing.T, lines []string) []string {
	t.Helper()
	pg, err := ReadPage(writeTranscript(t, lines), 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range pg.Messages {
		got = append(got, m.ID+"|"+m.Kind+"|"+m.From+"|"+m.Text)
	}
	return got
}

// AC1-AC3: every sender shape the rigs use is served as another agent's
// message, with its sender and the words after the bracket: typed, queued
// while the agent works, and pasted by initech send.
func TestTyped_EverySenderShapeInTheRigsIsAMessage(t *testing.T) {
	for i, c := range senderCorpus {
		body := fmt.Sprintf("VPN is down on spark%d.", i)
		lines := []string{
			tline("user", "t", t0, "[from "+c.sender+"] "+body),
			queuedPrompt("q", "[from "+c.sender+"] "+body),
			tline("user", "p", t0, "<pasted_content id=\"x\">\n[from "+c.sender+"] "+body+"\n</pasted_content>"),
		}
		got := senderPage(t, lines)
		want := []string{"t|message|" + c.sender + "|" + body, "q|message|" + c.sender + "|" + body, "p|message|" + c.sender + "|" + body}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s (%q):\n got %q\nwant %q", c.shape, c.sender, got, want)
		}
	}
}

// AC4: text that only looks like a sender stays the operator's.
func TestTyped_TextThatOnlyLooksLikeASenderStaysTheOperators(t *testing.T) {
	// 160 written out, not senderMax: a test that reads the bound it checks
	// moves with any change to it.
	long := strings.Repeat("x", 161)
	at := strings.Repeat("y", 160)
	cases := []struct{ name, text, want string }{
		{"never closed on the first line", "[from super of nayutal VPN is down\nsee ] above", "nelson|"},
		{"longer than the bound", "[from " + long + "] hi", "nelson|"},
		{"empty", "[from ] hi", "nelson|"},
		{"only spaces", "[from    ] hi", "nelson|"},
		{"no space after from", "[fromeng1] hi", "nelson|"},
		{"not at the start", "see [from eng1] hi", "nelson|"},
		// The positive twins: at the bound, and padded inside the bracket.
		{"exactly the bound", "[from " + at + "] hi", "message|" + at},
		{"padded", "[from  eng1 ] hi", "message|eng1"},
	}
	for _, c := range cases {
		got := senderPage(t, []string{tline("user", "u", t0, c.text)})
		if len(got) != 1 {
			t.Fatalf("%s: %q", c.name, got)
		}
		parts := strings.SplitN(got[0], "|", 4)
		if kf := parts[1] + "|" + parts[2]; kf != c.want {
			t.Errorf("%s: kind|from = %q, want %q", c.name, kf, c.want)
		}
		if strings.HasPrefix(c.want, "nelson") && parts[3] != strings.TrimSpace(c.text) {
			t.Errorf("%s: the operator's text changed: %q", c.name, parts[3])
		}
	}
}
