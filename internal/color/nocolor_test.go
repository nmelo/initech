package color

import "testing"

// TestHasNoColorArg verifies that hasNoColorArg detects --no-color in a slice
// of CLI arguments. This exercises the logic used by init() to disable colors
// before cobra parses flags, so that cobra parse errors print without ANSI codes.
func TestHasNoColorArg(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--no-color"}, true},
		{[]string{"--no-color", "--bad-flag"}, true},
		{[]string{"up", "--no-color"}, true},
		{[]string{"up", "--verbose"}, false},
		{[]string{}, false},
		{[]string{"-no-color"}, false},       // single dash not matched
		{[]string{"no-color"}, false},        // missing dashes not matched
		{[]string{"--no-color=true"}, false}, // with value not matched (cobra strips it)
	}
	for _, c := range cases {
		got := hasNoColorArg(c.args)
		if got != c.want {
			t.Errorf("hasNoColorArg(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
