//go:build linux

package tui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// systemMemoryTotal returns total physical RAM in kilobytes on Linux.
// Reads MemTotal from /proc/meminfo.
func systemMemoryTotal() (int64, error) {
	return readMeminfo("MemTotal")
}

// systemMemoryAvail returns available RAM in kilobytes on Linux.
// Reads MemAvailable from /proc/meminfo (kernel 3.14+).
func systemMemoryAvail() (int64, error) {
	return readMeminfo("MemAvailable")
}

// readMeminfo reads a single field from /proc/meminfo. Values are in kB.
func readMeminfo(field string) (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	prefix := field + ":"
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return 0, fmt.Errorf("unexpected format for %s: %s", field, line)
		}
		kb, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s value: %w", field, err)
		}
		return kb, nil
	}
	return 0, fmt.Errorf("%s not found in /proc/meminfo", field)
}
