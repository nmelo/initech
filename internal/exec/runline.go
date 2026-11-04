package exec

import (
	"context"
	"errors"
	osexec "os/exec"
	"time"
)

// RunLine runs one shell line in dir and returns its combined output and exit
// code. The context bounds it: on expiry the line's whole process group is
// stopped, so nothing it started outlives the bound, and the error is the
// context's. An exit code of -1 means the line did not run to an exit.
func RunLine(ctx context.Context, dir, line string) (string, int, error) {
	cmd := osexec.Command("sh", "-c", line)
	cmd.Dir = dir
	setProcessGroup(cmd)
	cmd.WaitDelay = 100 * time.Millisecond
	var out lineBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return "", -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *osexec.ExitError
		switch {
		case err == nil:
			return out.String(), 0, nil
		case errors.As(err, &exit):
			return out.String(), exit.ExitCode(), nil
		default:
			return out.String(), -1, err
		}
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done
		return out.String(), -1, ctx.Err()
	}
}
