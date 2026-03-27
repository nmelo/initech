//go:build darwin

package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// systemMemoryTotal returns total physical RAM in kilobytes on macOS.
// Uses sysctl hw.memsize which returns bytes.
func systemMemoryTotal() (int64, error) {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0, fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse hw.memsize: %w", err)
	}
	return bytes / 1024, nil // Convert bytes to KB.
}

// systemMemoryAvail returns available RAM in kilobytes on macOS.
// Parses vm_stat output: available = (free + inactive) * page_size.
func systemMemoryAvail() (int64, error) {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("vm_stat: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	var pageSize int64 = 4096 // Default page size on macOS.
	var free, inactive int64

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics:") {
			// Parse "page size of NNNN bytes" from the header.
			if idx := strings.Index(line, "page size of "); idx >= 0 {
				field := strings.Fields(line[idx:])[3]
				if ps, err := strconv.ParseInt(field, 10, 64); err == nil {
					pageSize = ps
				}
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "."))
		pages, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			continue
		}

		switch key {
		case "Pages free":
			free = pages
		case "Pages inactive":
			inactive = pages
		}
	}

	availBytes := (free + inactive) * pageSize
	return availBytes / 1024, nil // Convert bytes to KB.
}
