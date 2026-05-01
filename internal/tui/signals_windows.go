//go:build windows

package tui

import (
	"os"
	"os/signal"
	"sync"

	"github.com/gdamore/tcell/v2"
)

var exitFunc = func(code int) { os.Exit(code) }

func installSignalHandlers(screen tcell.Screen, quitCh chan struct{}, cleanupPaths ...string) func() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt)

	var exitOnce sync.Once

	go func() {
		sig, ok := <-ch
		if !ok {
			return
		}
		LogError("tui", "killed by signal", "signal", sig.String())
		for _, p := range cleanupPaths {
			os.Remove(p)
		}
		if screen != nil {
			screen.Fini()
		}
		exitOnce.Do(func() { exitFunc(2) })
	}()

	return func() {
		signal.Stop(ch)
		exitOnce.Do(func() {})
		for len(ch) > 0 {
			<-ch
		}
		close(ch)
	}
}
