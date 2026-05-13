//go:build !windows

package cmd

import (
	"fmt"
	"os"
)

func platformEnvironmentChecks() []checkResult {
	return nil
}

// checkConfigPermissions inspects initech.yaml's mode bits and warns if any
// group/other bit is set. The file stores auth tokens (announce_url,
// webhook_url, MCP bearers) so it should be readable only by the owner
// (0o600). The bitmask check '&0o077' is intentionally permissive about
// owner-execute bits: 0o700 (rwx for owner) is numerically greater than
// 0o600 but doesn't expose the file to other UIDs, so we don't warn on it
// — only on bits that actually let a non-owner read.
//
// Missing initech.yaml is not a defect for this check (that's covered
// elsewhere in doctor); we just return nil and stay silent.
//
// Some network filesystems (NFS, some SMB configurations) don't honor
// chmod; on those, this check will warn even after the user runs the
// suggested fix. Documented limitation, not handled.
func checkConfigPermissions(cfgPath string) []checkResult {
	info, err := os.Stat(cfgPath)
	if err != nil {
		return nil
	}
	mode := info.Mode().Perm()
	if mode&0o077 == 0 {
		return nil
	}
	return []checkResult{{
		Label:  "Config perms",
		Status: "WARN",
		Detail: fmt.Sprintf("initech.yaml is %#o, contains auth tokens — chmod 600 initech.yaml", mode),
	}}
}
