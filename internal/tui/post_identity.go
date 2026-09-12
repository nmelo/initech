// post_identity.go attributes inbox commands to the process tree of a pane.
// Environment and request fields are never identity evidence. This is an
// integrity boundary among same-UID processes, not a local security sandbox.
package tui

import (
	"fmt"
	"net"
	"runtime"
	"time"
)

const postIdentityRule = "posts are attributed to the agent that runs them; run initech post from inside your agent's pane"
const postWindowsRefusal = "post is not available on Windows in this release: initech cannot identify which agent is posting over this transport"

type postIdentity struct {
	agent  string
	runKey string
}

type postPaneProcess struct {
	pid      int
	identity postIdentity
}

func postPlatformError(platform string) error {
	switch platform {
	case "darwin", "linux":
		return nil
	case "windows":
		return fmt.Errorf("%s", postWindowsRefusal)
	default:
		return fmt.Errorf("post is not available on %s: connection identity is unsupported", platform)
	}
}

func (t *TUI) identifyPostConnection(conn net.Conn) (postIdentity, error) {
	if err := postPlatformError(runtime.GOOS); err != nil {
		return postIdentity{}, err
	}
	pid, err := postPeerPID(conn)
	if err != nil {
		return postIdentity{}, fmt.Errorf("%s; could not identify your pane: %w", postIdentityRule, err)
	}
	var processes []postPaneProcess
	if !t.runOnMain(func() {
		for _, pv := range t.panes {
			if p, ok := pv.(*Pane); ok {
				p.mu.Lock()
				if p.alive && !p.suspended && p.pid > 0 {
					processes = append(processes, postPaneProcess{pid: p.pid, identity: postIdentity{
						agent: p.name, runKey: postProcessRunKey(p.pid, p.startedAt),
					}})
				}
				p.mu.Unlock()
			}
		}
	}) {
		return postIdentity{}, fmt.Errorf("could not identify your pane: TUI shutting down")
	}
	return identifyPostAncestor(pid, processes, postParentPID)
}

// The pane process is stable across CLI invocations but changes on restart.
// Including its start instant also separates recycled OS PIDs.
func postProcessRunKey(pid int, started time.Time) string {
	return fmt.Sprintf("%d:%d", pid, started.UnixNano())
}

func identifyPostAncestor(pid int, panes []postPaneProcess, parent func(int) (int, error)) (postIdentity, error) {
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
	return postIdentity{}, fmt.Errorf("%s; could not identify your pane", postIdentityRule)
}
