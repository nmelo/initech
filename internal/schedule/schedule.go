// Package schedule is a rig's own crontab: the jobs in .initech/schedule.yaml,
// when each is due, and the file they live in. It knows nothing about panes or
// delivery; window 1's scheduler reads it and does the running.
//
// A job is a time of day (with an optional day pattern) or an interval, and
// either a message or a one-line command, for one pane. The last run is the
// guard against running a due time twice: a due time at or before it never
// fires again, which also makes a restart inside the same minute harmless.
package schedule

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FileName is the schedule's file inside a rig's .initech folder.
const FileName = "schedule.yaml"

// MinInterval is the shortest interval a job may have.
const MinInterval = time.Minute

// Job is one scheduled delivery.
type Job struct {
	ID         string     `yaml:"id"`
	Owner      string     `yaml:"owner"`
	To         string     `yaml:"to"`
	At         string     `yaml:"at,omitempty"`
	Every      string     `yaml:"every,omitempty"`
	Message    string     `yaml:"message,omitempty"`
	Command    string     `yaml:"command,omitempty"`
	Created    time.Time  `yaml:"created"`
	LastRun    *time.Time `yaml:"last_run,omitempty"`
	LastResult string     `yaml:"last_result,omitempty"`
	// Told is the delivery failure super was last told of, so a repeat of it
	// is said once; a delivery clears it.
	Told string `yaml:"told,omitempty"`
	// Line is where the job starts in the file it was read from; never written.
	Line int `yaml:"-"`
}

// File is the whole schedule.
type File struct {
	NextID int   `yaml:"next_id"`
	Jobs   []Job `yaml:"jobs"`
}

// Path is the schedule file of the rig rooted at root.
func Path(root string) string { return filepath.Join(root, ".initech", FileName) }

// clock is a parsed At: a time of day and the weekdays it runs on.
type clock struct {
	hour, min int
	days      [7]bool // indexed by time.Weekday
}

var dayNames = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// parseAt reads "HH:MM" with an optional day pattern: daily (the default),
// weekdays, weekends, or a comma list of mon..sun.
func parseAt(s string) (clock, error) {
	var c clock
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 2 {
		return c, fmt.Errorf("at %q: want HH:MM, optionally followed by daily, weekdays, weekends or days like mon,thu", s)
	}
	hm := strings.Split(fields[0], ":")
	if len(hm) != 2 || len(hm[0]) != 2 || len(hm[1]) != 2 {
		return c, fmt.Errorf("at %q: the time is HH:MM", s)
	}
	h, err1 := strconv.Atoi(hm[0])
	m, err2 := strconv.Atoi(hm[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return c, fmt.Errorf("at %q: the time is HH:MM, 00:00 to 23:59", s)
	}
	c.hour, c.min = h, m
	pattern := "daily"
	if len(fields) == 2 {
		pattern = strings.ToLower(fields[1])
	}
	switch pattern {
	case "daily":
		for i := range c.days {
			c.days[i] = true
		}
	case "weekdays":
		for d := time.Monday; d <= time.Friday; d++ {
			c.days[d] = true
		}
	case "weekends":
		c.days[time.Saturday], c.days[time.Sunday] = true, true
	default:
		for _, name := range strings.Split(pattern, ",") {
			d, ok := dayNames[name]
			if !ok {
				return c, fmt.Errorf("at %q: unknown day %q (mon, tue, wed, thu, fri, sat, sun)", s, name)
			}
			c.days[d] = true
		}
	}
	return c, nil
}

// prevDue is the latest due time at or before now, or zero if none in the
// past week.
func (c clock) prevDue(now time.Time) time.Time {
	for back := 0; back <= 7; back++ {
		d := now.AddDate(0, 0, -back)
		t := time.Date(d.Year(), d.Month(), d.Day(), c.hour, c.min, 0, 0, now.Location())
		if !t.After(now) && c.days[t.Weekday()] {
			return t
		}
	}
	return time.Time{}
}

// nextDue is the first due time after now.
func (c clock) nextDue(now time.Time) time.Time {
	for ahead := 0; ahead <= 8; ahead++ {
		d := now.AddDate(0, 0, ahead)
		t := time.Date(d.Year(), d.Month(), d.Day(), c.hour, c.min, 0, 0, now.Location())
		if t.After(now) && c.days[t.Weekday()] {
			return t
		}
	}
	return time.Time{}
}

// Validate reports what is wrong with a job, or nil.
func (j Job) Validate() error {
	switch {
	case strings.TrimSpace(j.To) == "":
		return errors.New("a job needs a pane to deliver to")
	case strings.TrimSpace(j.Owner) == "":
		return errors.New("a job needs an owner")
	case (j.At == "") == (j.Every == ""):
		return errors.New("a job has exactly one of at and every")
	case (j.Message == "") == (j.Command == ""):
		return errors.New("a job has exactly one of message and command")
	case strings.ContainsAny(j.Command, "\r\n"):
		return errors.New("a command is one line")
	}
	if j.At != "" {
		if _, err := parseAt(j.At); err != nil {
			return err
		}
	}
	if j.Every != "" {
		d, err := time.ParseDuration(j.Every)
		if err != nil {
			return fmt.Errorf("every %q: %v", j.Every, err)
		}
		if d < MinInterval {
			return fmt.Errorf("every %q: the shortest interval is one minute", j.Every)
		}
	}
	return nil
}

// base is the time a job's schedule counts from.
func (j Job) base() time.Time {
	if j.LastRun != nil && j.LastRun.After(j.Created) {
		return *j.LastRun
	}
	return j.Created
}

// Check decides whether j runs at now. started is when this initech started:
// a time job's due time before it was missed while initech was down, and runs
// once with missed set; an interval job counts from the later of its last run
// and the start, so it resumes rather than catching up. due is the due time a
// run claims. A due time at or before the last run never fires.
func (j Job) Check(now, started time.Time) (due time.Time, missed, fire bool) {
	if j.At != "" {
		c, err := parseAt(j.At)
		if err != nil {
			return time.Time{}, false, false
		}
		due = c.prevDue(now)
		if due.IsZero() || !due.After(j.base()) {
			return time.Time{}, false, false
		}
		return due, due.Before(started), true
	}
	every, err := time.ParseDuration(j.Every)
	if err != nil || every < MinInterval {
		return time.Time{}, false, false
	}
	from := j.base()
	if started.After(from) {
		from = started
	}
	due = from.Add(every)
	if now.Before(due) {
		return time.Time{}, false, false
	}
	return due, false, true
}

// Next is the job's next run after now, for listings.
func (j Job) Next(now, started time.Time) time.Time {
	if j.At != "" {
		c, err := parseAt(j.At)
		if err != nil {
			return time.Time{}
		}
		if due, _, fire := j.Check(now, started); fire {
			return due
		}
		return c.nextDue(now)
	}
	every, err := time.ParseDuration(j.Every)
	if err != nil {
		return time.Time{}
	}
	from := j.base()
	if started.After(from) {
		from = started
	}
	return from.Add(every)
}

// Add appends job with a fresh id and returns that id.
func (f *File) Add(job Job) (string, error) {
	if err := job.Validate(); err != nil {
		return "", err
	}
	f.NextID++
	job.ID = "s" + strconv.Itoa(f.NextID)
	f.Jobs = append(f.Jobs, job)
	return job.ID, nil
}

// Remove deletes the job with id when by owns it or by is super, and returns
// what was removed. Anyone else is refused with the owner named.
func (f *File) Remove(id, by string) (Job, error) {
	for i, j := range f.Jobs {
		if j.ID != id {
			continue
		}
		if by != j.Owner && by != "super" {
			return Job{}, fmt.Errorf("job %s belongs to %s: only %s or super removes it", id, j.Owner, j.Owner)
		}
		f.Jobs = append(f.Jobs[:i:i], f.Jobs[i+1:]...)
		return j, nil
	}
	return Job{}, fmt.Errorf("no job %s", id)
}

// Load reads the schedule at path. A missing file is an empty schedule. A file
// that does not parse, or holds a job that is not valid, is an error naming the
// line, so the caller keeps its last good schedule.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(path, data)
}

func parse(path string, data []byte) (*File, error) {
	f := &File{}
	if len(bytes.TrimSpace(data)) == 0 {
		return f, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if err := doc.Decode(f); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	// Name the line of the first job that is not valid.
	var jobsNode *yaml.Node
	if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		m := doc.Content[0]
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "jobs" {
				jobsNode = m.Content[i+1]
			}
		}
	}
	seen := map[string]bool{}
	for i, j := range f.Jobs {
		line := 0
		if jobsNode != nil && i < len(jobsNode.Content) {
			line = jobsNode.Content[i].Line
		}
		f.Jobs[i].Line = line
		if err := j.Validate(); err != nil {
			return nil, fmt.Errorf("%s: line %d: job %q: %v", path, line, j.ID, err)
		}
		if j.ID == "" || seen[j.ID] {
			return nil, fmt.Errorf("%s: line %d: a job needs an id of its own", path, line)
		}
		seen[j.ID] = true
	}
	return f, nil
}

// Save writes the schedule to path atomically, readable by the owner only.
func Save(path string, f *File) error {
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+FileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // No-op after the rename.
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
