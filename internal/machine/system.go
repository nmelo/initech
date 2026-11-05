package machine

import (
	"fmt"
	"strconv"
	"strings"
)

// parseLoad5 reads the 5-minute figure from a load average line: macOS
// sysctl prints "{ 7.50 8.84 9.37 }", Linux /proc/loadavg "7.50 8.84 9.37
// 2/900 1234". The second number is the 5-minute one.
func parseLoad5(s string) (float64, error) {
	f := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(s))
	if len(f) < 2 {
		return 0, fmt.Errorf("load average %q: fewer than two figures", s)
	}
	v, err := strconv.ParseFloat(f[1], 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("load average %q: %q is not a load", s, f[1])
	}
	return v, nil
}
