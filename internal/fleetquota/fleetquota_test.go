package fleetquota

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ini-kb4c: fleet quota is the mean of every account's sevenDay.utilization
// (an integer percent) in cla's usage.json; a dash when that cannot be
// trusted. Nothing but the two pinned fields is decoded, and no account name,
// path or other value is logged.

var polled = time.Date(2026, 11, 5, 16, 40, 0, 0, time.UTC) // the fixture's polledAt

func fixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The fixture's seven accounts hold 60 70 80 90 50 40 34 (synthetic): mean 60.57, 61%.
func TestRead_TheMeanOfEveryAccountsSevenDayUtilization(t *testing.T) {
	q := Read(fixture(t), polled.Add(time.Minute))
	if !q.Known || q.Percent != 61 {
		t.Fatalf("quota = %+v, want 61%%", q)
	}
	if c := q.Cells(); c != 2 {
		t.Fatalf("cells = %d, want 2 at 61%%", c)
	}
}

func TestCells_FourCellsOverTheWholeRange(t *testing.T) {
	for p, want := range map[int]int{0: 0, 12: 0, 13: 1, 37: 1, 38: 2, 62: 2, 63: 3, 87: 3, 88: 4, 100: 4} {
		if got := (Quota{Percent: p, Known: true}).Cells(); got != want {
			t.Errorf("%d%%: %d cells, want %d", p, got, want)
		}
	}
	if (Quota{}).Cells() != 0 {
		t.Error("an unknown quota has cells")
	}
}

// Stale: the newest polledAt over 10 minutes old is a dash; at 9m59s it reads.
func TestRead_StaleOverTenMinutesByItsOwnTime(t *testing.T) {
	p := fixture(t)
	if q := Read(p, polled.Add(10*time.Minute+time.Second)); q.Known {
		t.Fatalf("10m01s old read as %d%%", q.Percent)
	}
	if q := Read(p, polled.Add(10*time.Minute-time.Second)); !q.Known {
		t.Fatalf("9m59s old was a dash: %s", q.Why)
	}
}

// The newest polledAt counts: one fresh account keeps the file fresh.
func TestRead_TheNewestPolledAtCounts(t *testing.T) {
	p := write(t, `{"a":{"sevenDay":{"utilization":50},"polledAt":"2026-11-05T16:00:00.000000+00:00"},
	"b":{"sevenDay":{"utilization":70},"polledAt":"2026-11-05T16:40:00.000000+00:00"}}`)
	if q := Read(p, polled.Add(5*time.Minute)); !q.Known || q.Percent != 60 {
		t.Fatalf("quota = %+v, want 60%%", q)
	}
}

// With no polledAt anywhere, the file's mtime stands in.
func TestRead_NoPolledAtFallsBackToTheFilesTime(t *testing.T) {
	p := write(t, `{"a":{"sevenDay":{"utilization":40}}}`)
	old := polled.Add(-20 * time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if q := Read(p, polled); q.Known {
		t.Fatalf("a 20-minute-old file read as %d%%", q.Percent)
	}
	if err := os.Chtimes(p, polled, polled); err != nil {
		t.Fatal(err)
	}
	if q := Read(p, polled.Add(time.Minute)); !q.Known || q.Percent != 40 {
		t.Fatalf("a fresh file = %+v", q)
	}
}

// Feed it nothing, and each other dash case.
func TestRead_EachDashCase(t *testing.T) {
	cases := map[string]string{
		"missing":          filepath.Join(t.TempDir(), "absent.json"),
		"unparsable":       write(t, `{not json`),
		"no accounts":      write(t, `{}`),
		"no field":         write(t, `{"a":{"fiveHour":{"utilization":10},"polledAt":"2026-11-05T16:40:00.000000+00:00"}}`),
		"null utilization": write(t, `{"a":{"sevenDay":{"utilization":null},"polledAt":"2026-11-05T16:40:00.000000+00:00"}}`),
		"not an object":    write(t, `[1,2,3]`),
	}
	for name, p := range cases {
		q := Read(p, polled.Add(time.Minute))
		if q.Known || q.Why == "" {
			t.Errorf("%s: %+v, want a dash with a reason", name, q)
		}
	}
}

// An account without the field is left out of the mean, not counted as 0.
func TestRead_AnAccountWithoutTheFieldIsLeftOut(t *testing.T) {
	p := write(t, `{"a":{"sevenDay":{"utilization":80},"polledAt":"2026-11-05T16:40:00.000000+00:00"},
	"b":{"polledAt":"2026-11-05T16:40:00.000000+00:00"}}`)
	if q := Read(p, polled.Add(time.Minute)); !q.Known || q.Percent != 80 {
		t.Fatalf("quota = %+v, want 80%%", q)
	}
}

// AC3: the decoded type carries exactly the two pinned fields.
func TestAccount_DecodesOnlyThePinnedFields(t *testing.T) {
	at := reflect.TypeOf(account{})
	var names []string
	for i := 0; i < at.NumField(); i++ {
		names = append(names, at.Field(i).Tag.Get("json"))
	}
	if strings.Join(names, ",") != "sevenDay,polledAt" {
		t.Fatalf("account decodes %v, want sevenDay,polledAt", names)
	}
	st := reflect.TypeOf(sevenDay{})
	if st.NumField() != 1 || st.Field(0).Tag.Get("json") != "utilization" {
		t.Fatalf("sevenDay decodes more than utilization: %v", st)
	}
}

const tokenish = "sk-ant-oat01-PLACEHOLDERtokenSHAPEDvalue0000000000"

// AC3: a token-shaped extra field is neither kept nor logged, and no log
// line names an account or the path.
func TestPoller_LogsNoTokenNameOrPath(t *testing.T) {
	p := write(t, `{"secret-person":{"accessToken":"`+tokenish+`","sevenDay":{"utilization":50},"polledAt":"2026-11-05T16:40:00.000000+00:00"}}`)
	var logs []string
	pl := NewPoller(p, func(s string) { logs = append(logs, s) })
	q := pl.Poll(polled.Add(time.Minute))
	if !q.Known || q.Percent != 50 {
		t.Fatalf("quota = %+v", q)
	}
	pl.Poll(polled.Add(20 * time.Minute)) // stale: one line
	if len(logs) != 1 {
		t.Fatalf("logs %q, want one stale line", logs)
	}
	for _, l := range logs {
		for _, bad := range []string{tokenish, "secret-person", p, filepath.Base(filepath.Dir(p))} {
			if strings.Contains(l, bad) {
				t.Fatalf("log %q carries %q", l, bad)
			}
		}
	}
	if s := strings.Join([]string{q.Why}, ""); strings.Contains(s, "secret-person") || strings.Contains(s, tokenish) {
		t.Fatalf("Why carries a name or token: %q", s)
	}
}

// Feed it nothing: no file shows a dash and logs one line, once, not every
// poll; when it comes back and goes again it logs again.
func TestPoller_NoFileLogsOnceAMinuteAtMost(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "usage.json")
	var logs []string
	pl := NewPoller(p, func(s string) { logs = append(logs, s) })
	for m := 0; m < 5; m++ {
		if q := pl.Poll(polled.Add(time.Duration(m) * time.Minute)); q.Known {
			t.Fatal("a missing file read as a number")
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "missing") {
		t.Fatalf("logs %q, want one missing line", logs)
	}
	os.WriteFile(p, []byte(`{"a":{"sevenDay":{"utilization":30},"polledAt":"`+polled.Add(5*time.Minute).Format(time.RFC3339Nano)+`"}}`), 0o600)
	if q := pl.Poll(polled.Add(6 * time.Minute)); !q.Known || q.Percent != 30 {
		t.Fatalf("back: %+v", q)
	}
	os.Remove(p)
	pl.Poll(polled.Add(8 * time.Minute))
	if len(logs) != 2 {
		t.Fatalf("logs %q, want a second line after it came back and went", logs)
	}
}

// The read gate: at most one read a minute; between reads the last quota
// stands.
func TestPoller_ReadsAtMostOnceAMinute(t *testing.T) {
	p := fixture(t)
	reads := 0
	pl := NewPoller(p, nil)
	pl.read = func(path string, now time.Time) Quota { reads++; return Read(path, now) }
	for s := 0; s < 180; s += 7 {
		pl.Poll(polled.Add(time.Duration(s) * time.Second))
	}
	if reads != 3 {
		t.Fatalf("%d reads over three minutes of polls, want 3", reads)
	}
	if q := pl.Last(); !q.Known || q.Percent != 61 {
		t.Fatalf("last = %+v", q)
	}
}

// DefaultPath is cla's file under the user's home.
func TestDefaultPath_IsClasUsageFile(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	if got, err := DefaultPath(); err != nil || got != "/home/someone/.claude-accounts/usage.json" {
		t.Fatalf("DefaultPath = %q, %v", got, err)
	}
}
