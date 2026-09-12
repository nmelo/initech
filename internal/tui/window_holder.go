package tui

import (
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"time"

	iexec "github.com/nmelo/initech/internal/exec"
)

// inspectListenerHolder asks the OS who actually holds the listener. It never
// uses initech.pid: a newer main already overwrote it in the reported failure.
// Missing inspection tools or inaccessible processes leave the holder unknown.
func inspectListenerHolder(r iexec.Runner, address string, now time.Time) *PortHolder {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil
	}
	if runtime.GOOS == "windows" {
		// Only a validated integer is interpolated; no project data is code.
		script := fmt.Sprintf(`Get-NetTCPConnection -State Listen -LocalPort %d -ErrorAction SilentlyContinue | ForEach-Object { $p = Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue; if ($p) { [pscustomobject]@{PID=$p.Id;Name=$p.ProcessName;Address=$_.LocalAddress;Started=$p.StartTime.ToUniversalTime().ToString('o')} } } | ConvertTo-Json -Compress`, n)
		out, err := r.Run("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		if err != nil {
			return nil
		}
		return parseWindowsHolder(out, host)
	}
	out, err := r.Run("lsof", "-nP", "-a", "-iTCP:"+port, "-sTCP:LISTEN", "-Fpcn")
	if err != nil {
		return nil
	}
	holder := parseLsofHolder(out, host, port)
	if holder == nil {
		return nil
	}
	// ps verifies the PID is still present, obtains the untruncated executable
	// name, and reports elapsed time on both macOS and Linux.
	out, err = r.Run("ps", "-p", strconv.Itoa(holder.PID), "-o", "etime=", "-o", "comm=")
	if err != nil {
		return nil
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return nil
	}
	if elapsed, ok := parseElapsed(fields[0]); ok {
		holder.StartedAt = now.Add(-elapsed)
	}
	holder.Name = strings.Join(fields[1:], " ")
	return holder
}

func bindHostsOverlap(want, got string) bool {
	if want == "" || want == "*" || want == "0.0.0.0" || want == "::" || got == "*" || got == "0.0.0.0" || got == "::" {
		return true
	}
	if want == "localhost" {
		return net.ParseIP(got).IsLoopback()
	}
	a, b := net.ParseIP(want), net.ParseIP(got)
	return want == got || (a != nil && b != nil && a.Equal(b))
}

func parseLsofHolder(out, host, port string) *PortHolder {
	var current PortHolder
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			current = PortHolder{}
			current.PID, _ = strconv.Atoi(line[1:])
		case 'c':
			current.Name = line[1:]
		case 'n':
			h, p, err := net.SplitHostPort(strings.TrimSuffix(line[1:], " (LISTEN)"))
			if err == nil && p == port && bindHostsOverlap(host, h) && current.PID > 0 {
				copy := current
				return &copy
			}
		}
	}
	return nil
}

func parseWindowsHolder(out, host string) *PortHolder {
	type entry struct {
		PID                    int
		Name, Address, Started string
	}
	var entries []entry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		var one entry
		if err := json.Unmarshal([]byte(out), &one); err != nil {
			return nil
		}
		entries = []entry{one}
	}
	for _, e := range entries {
		if e.PID <= 0 || !bindHostsOverlap(host, e.Address) {
			continue
		}
		start, _ := time.Parse(time.RFC3339Nano, e.Started)
		return &PortHolder{PID: e.PID, Name: e.Name, StartedAt: start}
	}
	return nil
}

// parseElapsed accepts ps etime's [[days-]hours:]minutes:seconds format.
func parseElapsed(s string) (time.Duration, bool) {
	days := 0
	if left, right, ok := strings.Cut(s, "-"); ok {
		var err error
		days, err = strconv.Atoi(left)
		if err != nil || days < 0 {
			return 0, false
		}
		s = right
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	seconds := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, false
		}
		seconds = seconds*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(seconds)*time.Second, true
}
