package appserve

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// BinaryEvery is how often the service looks at the initech it runs.
const BinaryEvery = time.Minute

// DrainGrace is how long requests in flight get to finish once the service
// has decided to exit for a new build.
const DrainGrace = 5 * time.Second

// ArmBinaryWatch is where path, the executable as launched (os.Executable:
// the link launchd ran, unresolved), resolves now, and true, when it is a
// link that resolves. An upgrade moves that link to a new build. A path that
// is not a link (a dev build run by its own path) or does not resolve
// arms nothing, said once in log.
func ArmBinaryWatch(path string, lstat func(string) (os.FileInfo, error), resolve func(string) (string, error), log io.Writer) (string, bool) {
	fi, err := lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		fmt.Fprintf(log, "initech app-serve: %s is not a link, so an upgrade does not restart the service\n", path)
		return "", false
	}
	start, err := resolve(path)
	if err != nil {
		fmt.Fprintf(log, "initech app-serve: %s does not resolve (%v), so an upgrade does not restart the service\n", path, err)
		return "", false
	}
	return start, true
}

// BinaryReplaced waits until path resolves to a file other than start, read
// once per tick, and returns that file; "" when ctx ends first. A path that
// does not resolve (mid-upgrade) or resolves to start is nothing.
func BinaryReplaced(ctx context.Context, path, start string, resolve func(string) (string, error), tick <-chan time.Time) string {
	for {
		select {
		case <-ctx.Done():
			return ""
		case <-tick:
		}
		now, err := resolve(path)
		if err != nil || now == start {
			continue
		}
		return now
	}
}

// ReplacedLine is the one log line for a replaced build.
func ReplacedLine(from, to string) string {
	return fmt.Sprintf("initech app-serve: the initech it runs was replaced (%s -> %s); finishing requests in flight and exiting, and launchd starts the new build", from, to)
}
