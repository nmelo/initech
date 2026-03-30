package cmd

import (
	"testing"
	"time"
)

func TestParseAtTime_24Hour(t *testing.T) {
	got, err := parseAtTime("14:30")
	if err != nil {
		t.Fatalf("parseAtTime(14:30): %v", err)
	}
	now := time.Now()
	if got.Hour() != 14 || got.Minute() != 30 {
		t.Errorf("got %v, want 14:30 today", got)
	}
	if got.Year() != now.Year() || got.Month() != now.Month() || got.Day() != now.Day() {
		t.Errorf("date should be today, got %v", got)
	}
}

func TestParseAtTime_12Hour(t *testing.T) {
	got, err := parseAtTime("2:30pm")
	if err != nil {
		t.Fatalf("parseAtTime(2:30pm): %v", err)
	}
	if got.Hour() != 14 || got.Minute() != 30 {
		t.Errorf("got %v, want 14:30", got)
	}
}

func TestParseAtTime_FullDate(t *testing.T) {
	got, err := parseAtTime("2026-04-01 09:00")
	if err != nil {
		t.Fatalf("parseAtTime(full date): %v", err)
	}
	if got.Year() != 2026 || got.Month() != 4 || got.Day() != 1 {
		t.Errorf("date = %v, want 2026-04-01", got)
	}
	if got.Hour() != 9 || got.Minute() != 0 {
		t.Errorf("time = %v, want 09:00", got)
	}
}

func TestParseAtTime_Invalid(t *testing.T) {
	_, err := parseAtTime("not-a-time")
	if err == nil {
		t.Error("expected error for invalid time")
	}
}

func TestRunAt_MutuallyExclusive(t *testing.T) {
	atIn = "5m"
	atAt = "14:00"
	defer func() { atIn = ""; atAt = "" }()

	err := runAt(atCmd, []string{"eng1", "test"})
	if err == nil || err.Error() != "cannot use both --in and --at" {
		t.Errorf("expected mutual exclusion error, got: %v", err)
	}
}

func TestRunAt_RequiresTimeFlag(t *testing.T) {
	atIn = ""
	atAt = ""
	err := runAt(atCmd, []string{"eng1", "test"})
	if err == nil || err.Error() != "must specify --in or --at" {
		t.Errorf("expected missing time error, got: %v", err)
	}
}

func TestRunAt_RequiresArgs(t *testing.T) {
	atIn = "5m"
	defer func() { atIn = "" }()
	err := runAt(atCmd, []string{"eng1"})
	if err == nil {
		t.Error("expected error with only 1 arg")
	}
}
