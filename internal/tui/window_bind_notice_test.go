package tui

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

func TestWindowListener_HeldPortPersistsNoticeAndWarnsOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	root := t.TempDir()
	stopLog := InitLogger(root, slog.LevelInfo)
	defer stopLog()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(180, 40)
	u := newTestTUI()
	u.screen = s
	cleanup := u.startWindowListener(&config.Project{WindowListen: ln.Addr().String()}, "test", nil)
	defer cleanup()
	if u.windowSrv != nil {
		t.Fatal("held port unexpectedly bound")
	}
	for frame := 0; frame < 30; frame++ {
		u.render()
		// The OS-reported executable path can move the remedy across a
		// row boundary; inspect the complete wrapped surface.
		text := strings.ReplaceAll(readScreenRect(s, 0, 0, 180, 40), "\n", "")
		for _, want := range []string{"WINDOW PORT UNAVAILABLE", ln.Addr().String(), u.windowPort.Reason, "restart this main"} {
			if !strings.Contains(text, want) {
				t.Errorf("frame %d missing %q", frame, want)
				break
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".initech", "initech.log"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "level=WARN msg=\"[window-server] bind failed\""); n != 1 {
		t.Errorf("bind failure WARN count = %d, want 1", n)
	}
}
