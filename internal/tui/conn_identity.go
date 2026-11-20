// conn_identity.go attributes a command (cron, watch, strip, a serve send) to
// the agent whose pane's process tree runs it.
// Environment and request fields are never identity evidence. This is an
// integrity boundary among same-UID processes, not a local security sandbox.
package tui

import (
	"fmt"
	"net"
	"runtime"
	"time"
)

// connIdentityRule and the platform refusals name no command: the check is
// shared, and each command answers in these words or its own.
const connIdentityRule = "this command acts for the agent whose pane runs it; run it from inside that agent's pane"
const connWindowsRefusal = "this command is not available on Windows in this release: initech cannot tell which agent's pane a call comes from over this transport"

type connIdentity struct {
	agent  string
	runKey string
}

type connPaneProcess struct {
	pid      int
	identity connIdentity
}

func connPlatformError(platform string) error {
	switch platform {
	case "darwin", "linux":
		return nil
	case "windows":
		return fmt.Errorf("%s", connWindowsRefusal)
	default:
		return fmt.Errorf("this command is not available on %s: initech cannot tell which agent pane a call comes from there", platform)
	}
}

func (t *TUI) identifyConnection(conn net.Conn) (connIdentity, error) {
	if err := connPlatformError(runtime.GOOS); err != nil {
		return connIdentity{}, err
	}
	pid, err := connPeerPID(conn)
	if err != nil {
		return connIdentity{}, fmt.Errorf("%s; could not identify your pane: %w", connIdentityRule, err)
	}
	var processes []connPaneProcess
	if !t.runOnMain(func() {
		for _, pv := range t.panes {
			if p, ok := pv.(*Pane); ok {
				p.mu.Lock()
				if p.alive && !p.suspended && p.pid > 0 {
					processes = append(processes, connPaneProcess{pid: p.pid, identity: connIdentity{
						agent: p.name, runKey: connProcessRunKey(p.pid, p.startedAt),
					}})
				}
				p.mu.Unlock()
			}
		}
	}) {
		return connIdentity{}, fmt.Errorf("could not identify your pane: TUI shutting down")
	}
	return identifyAncestor(pid, processes, connParentPID)
}

// The pane process is stable across CLI invocations but changes on restart.
// Including its start instant also separates recycled OS PIDs.
func connProcessRunKey(pid int, started time.Time) string {
	return fmt.Sprintf("%d:%d", pid, started.UnixNano())
}

func identifyAncestor(pid int, panes []connPaneProcess, parent func(int) (int, error)) (connIdentity, error) {
	visited := make(map[int]bool)
	deadline := time.Now().Add(time.Second)
	for depth := 0; pid > 1 && depth < 128 && time.Now().Before(deadline); depth++ {
		if visited[pid] {
			break
		}
		visited[pid] = true
		for _, p := range panes {
			if p.pid == pid {
				return p.identity, nil
			}
		}
		var err error
		pid, err = parent(pid)
		if err != nil {
			break
		}
	}
	return connIdentity{}, fmt.Errorf("%s; could not identify your pane", connIdentityRule)
}
