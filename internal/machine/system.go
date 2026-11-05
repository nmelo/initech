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

// parseVMStat reads macOS vm_stat output into reclaimable memory in KB: free
// plus inactive plus speculative pages, at the page size its header states.
// Free pages alone stay near zero on a healthy Mac, which keeps file cache in
// inactive pages. A missing line is an error, never a guess: an unreadable
// number does not engage the ceiling.
func parseVMStat(out string) (int64, error) {
	pageSize, pages := int64(0), map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		if _, rest, ok := strings.Cut(line, "page size of "); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				pageSize, _ = strconv.ParseInt(f[0], 10, 64)
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key = strings.TrimSpace(key); key {
		case "Pages free", "Pages inactive", "Pages speculative":
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("vm_stat: %s: %q is not a page count", key, val)
			}
			pages[key] = n
		}
	}
	if pageSize <= 0 {
		return 0, fmt.Errorf("vm_stat: no page size")
	}
	total := int64(0)
	for _, key := range []string{"Pages free", "Pages inactive", "Pages speculative"} {
		n, ok := pages[key]
		if !ok {
			return 0, fmt.Errorf("vm_stat: no %q line", key)
		}
		total += n
	}
	return total * pageSize / 1024, nil
}

// meminfoKB reads one "Key: <n> kB" line of Linux /proc/meminfo.
func meminfoKB(out, key string) (int64, error) {
	for _, line := range strings.Split(out, "\n") {
		k, val, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		f := strings.Fields(val)
		if len(f) == 0 {
			break
		}
		n, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("meminfo: %s: %q is not a size", key, val)
		}
		return n, nil
	}
	return 0, fmt.Errorf("meminfo: no %s line", key)
}

// parseMeminfoReclaimable is reclaimable memory in KB from /proc/meminfo: the
// kernel's MemAvailable, never the bare MemFree.
func parseMeminfoReclaimable(out string) (int64, error) {
	return meminfoKB(out, "MemAvailable")
}

// parseMeminfoSwapUsed is swap in use in KB from /proc/meminfo.
func parseMeminfoSwapUsed(out string) (int64, error) {
	total, err := meminfoKB(out, "SwapTotal")
	if err != nil {
		return 0, err
	}
	free, err := meminfoKB(out, "SwapFree")
	if err != nil {
		return 0, err
	}
	if free > total {
		return 0, fmt.Errorf("meminfo: SwapFree above SwapTotal")
	}
	return total - free, nil
}

// parseSwapUsage is swap in use in KB from macOS sysctl vm.swapusage, as
// "total = 2048.00M  used = 1024.50M  free = 1023.50M  (encrypted)".
func parseSwapUsage(s string) (int64, error) {
	_, rest, ok := strings.Cut(s, "used = ")
	if !ok {
		return 0, fmt.Errorf("vm.swapusage %q: no used figure", s)
	}
	f := strings.Fields(rest)
	if len(f) == 0 || len(f[0]) < 2 {
		return 0, fmt.Errorf("vm.swapusage %q: no used figure", s)
	}
	num, unit := f[0][:len(f[0])-1], f[0][len(f[0])-1]
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("vm.swapusage %q: %q is not a size", s, f[0])
	}
	switch unit {
	case 'K':
	case 'M':
		v *= 1024
	case 'G':
		v *= 1024 * 1024
	default:
		return 0, fmt.Errorf("vm.swapusage %q: unknown unit in %q", s, f[0])
	}
	return int64(v), nil
}
