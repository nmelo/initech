package tui

import (
	"strings"
	"testing"
)

// TestShellQuoteArgs verifies that shellQuoteArgs produces output that is safe
// from shell injection when passed to sh -c.
func TestShellQuoteArgs(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		want  string
	}{
		{
			name: "simple args",
			args: []string{"claude", "--model", "sonnet"},
			want: "'claude' '--model' 'sonnet'",
		},
		{
			name: "arg with spaces",
			args: []string{"claude", "--flag", "value with spaces"},
			want: "'claude' '--flag' 'value with spaces'",
		},
		{
			name: "arg with single quote injection",
			args: []string{"claude", "--flag", "value'; rm -rf /; echo '"},
			want: `'claude' '--flag' 'value'"'"'; rm -rf /; echo '"'"''`,
		},
		{
			name: "arg with dollar sign",
			args: []string{"claude", "$(evil)"},
			want: "'claude' '$(evil)'",
		},
		{
			name: "arg with backtick",
			args: []string{"claude", "`evil`"},
			want: "'claude' '`evil`'",
		},
		{
			name: "empty args list",
			args: []string{},
			want: "",
		},
		{
			name: "single arg",
			args: []string{"claude"},
			want: "'claude'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shellQuoteArgs(tc.args)
			if got != tc.want {
				t.Errorf("shellQuoteArgs(%v):\n  got  %q\n  want %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestShellQuoteArgs_EscapeSequencePresent verifies that single quotes in args
// are properly escaped with the '"'"' sequence so they can't break out of
// single-quote quoting.
func TestShellQuoteArgs_EscapeSequencePresent(t *testing.T) {
	// A crafted input that would escape a naive quoting scheme.
	malicious := []string{"prog", "'; echo INJECTED; echo '"}
	result := shellQuoteArgs(malicious)

	// Every single quote in the original arg must be escaped as '"'"'
	// so that the shell cannot interpret it as an argument boundary.
	if !strings.Contains(result, `'"'"'`) {
		t.Errorf("expected single-quote escape sequence in output: %q", result)
	}
	// The result must still preserve the original content as literal text.
	if !strings.Contains(result, "INJECTED") {
		t.Errorf("original content missing from quoted output: %q", result)
	}
}

// TestSocketPath_Security verifies the socket is placed in .initech/ when projectRoot is set.
func TestSocketPath_Security(t *testing.T) {
	t.Run("with root", func(t *testing.T) {
		got := SocketPath("/home/user/project", "myproject")
		want := "/home/user/project/.initech/initech.sock"
		if got != want {
			t.Errorf("SocketPath with root: got %q, want %q", got, want)
		}
	})

	t.Run("empty root falls back to tmp", func(t *testing.T) {
		got := SocketPath("", "myproject")
		if !strings.HasPrefix(got, "/tmp/") {
			t.Errorf("SocketPath empty root: expected /tmp/ prefix, got %q", got)
		}
		if !strings.Contains(got, "myproject") {
			t.Errorf("SocketPath empty root: expected project name in path, got %q", got)
		}
	})
}
