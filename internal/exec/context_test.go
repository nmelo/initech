package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunContext_HelperProcess(t *testing.T) {
	if os.Getenv("INITECH_CONTEXT_RUNNER_HELPER") != "1" {
		return
	}
	fmt.Print("helper reached\n")
	mode := os.Args[len(os.Args)-1]
	if mode == "pipe" {
		child := osexec.Command(os.Args[0], "-test.run=^TestRunContext_HelperProcess$", "--", "child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Printf("inherited-pid=%d\n", child.Process.Pid)
	}
	if mode == "child" {
		time.Sleep(3 * time.Second)
	}
	if mode == "hang" {
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}

func TestDefaultRunner_RunContextBoundsInheritedOutputPipe(t *testing.T) {
	t.Setenv("INITECH_CONTEXT_RUNNER_HELPER", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := (&DefaultRunner{}).RunContext(context.Background(), binary, "-test.run=^TestRunContext_HelperProcess$", "--", "pipe")
	// The helper identifies only the child this test created. Clean it up
	// even when an assertion fails; never leave the sleeping fixture behind.
	for _, field := range strings.Fields(out) {
		if text, ok := strings.CutPrefix(field, "inherited-pid="); ok {
			pid, parseErr := strconv.Atoi(text)
			if parseErr == nil && pid > 0 {
				child, findErr := os.FindProcess(pid)
				if findErr == nil {
					defer child.Release()
					_ = child.Kill()
				}
			}
		}
	}
	if !strings.Contains(out, "inherited-pid=") {
		t.Fatalf("pipe inheritance not reached: %s", out)
	}
	if !errors.Is(err, osexec.ErrWaitDelay) {
		t.Fatalf("want bounded pipe drain, got %v: %s", err, out)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("inherited pipe held command open")
	}
}

func TestDefaultRunner_RunContextSuccessAndCancellation(t *testing.T) {
	t.Setenv("INITECH_CONTEXT_RUNNER_HELPER", "1")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := &DefaultRunner{}
	out, err := r.RunContext(context.Background(), binary, "-test.run=^TestRunContext_HelperProcess$", "--", "success")
	if err != nil || out != "helper reached" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	out, err = r.RunContext(ctx, binary, "-test.run=^TestRunContext_HelperProcess$", "--", "hang")
	if err == nil || ctx.Err() == nil {
		t.Fatalf("hung helper was not cancelled: %v", err)
	}
	if !strings.Contains(out, "helper reached") {
		t.Fatal("instrument did not reach the hung child")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancellation did not bound execution")
	}
}

func TestDefaultRunner_RunContextMissingBinary(t *testing.T) {
	out, err := (&DefaultRunner{}).RunContext(context.Background(), filepath.Join(t.TempDir(), "absent-claude"), "--version")
	if err == nil || out != "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
