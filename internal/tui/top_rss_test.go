package tui

import "testing"

func TestFormatTotalRSS(t *testing.T) {
	cases := []struct {
		kb   int64
		want string
	}{
		{1, "1 KB"},
		{512, "512 KB"},
		{1023, "1023 KB"},
		{1024, "1 MB"},
		{2048, "2 MB"},
		{10240, "10 MB"},
		{1048576, "1024 MB"}, // exactly at GB threshold (not >, so MB)
		{1048577, "1.0 GB"},  // one KB over threshold → GB
		{2097152, "2.0 GB"},
	}
	for _, c := range cases {
		got := formatTotalRSS(c.kb)
		if got != c.want {
			t.Errorf("formatTotalRSS(%d) = %q, want %q", c.kb, got, c.want)
		}
	}
}

// TestFormatTotalRSS_SmallTotal verifies the fix for the "0 MB" bug:
// values under 1024 KB now display in KB, not as "0 MB".
func TestFormatTotalRSS_SmallTotal(t *testing.T) {
	got := formatTotalRSS(512)
	if got != "512 KB" {
		t.Errorf("formatTotalRSS(512) = %q, want \"512 KB\" (was \"0 MB\" before fix)", got)
	}
}
