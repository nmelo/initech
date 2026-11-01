package appserve

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// ini-unf AC3: a copy of a signed binary keeps its signature: codesign
// --verify --strict passes on it and it reads back the source's identifier
// and team. An Apple platform binary always; the release binary when this
// Mac has one (shipper runs the release check in AC6).
func TestInstallCopy_ASignedBinaryKeepsItsSignature(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("code signatures are read with macOS codesign")
	}
	srcs := []string{"/usr/bin/true"}
	if rel, err := filepath.EvalSymlinks("/opt/homebrew/bin/initech"); err == nil {
		srcs = append(srcs, rel)
	} else {
		t.Log("no installed initech here: the release identity is checked by shipper on the release binary (AC6)")
	}
	for _, src := range srcs {
		want, err := ReadSignature(SystemCodesign, src)
		if err != nil || !want.Signed {
			t.Fatalf("%s: %+v %v, want a signed binary", src, want, err)
		}
		dst := filepath.Join(t.TempDir(), "app-serve", "initech")
		if err := InstallCopy(SystemCodesign, src, dst); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if out, err := SystemCodesign("--verify", "--strict", dst); err != nil {
			t.Errorf("%s: the copy does not verify: %v %s", src, err, out)
		}
		if got, err := ReadSignature(SystemCodesign, dst); err != nil || got != want {
			t.Errorf("%s: the copy is signed %+v (%v), want %+v", src, got, err, want)
		}
	}
}

// ini-unf AC4: replacing the copy never leaves the fixed path missing or
// half written: a reader looking at it throughout sees the old bytes or the
// new, never neither.
func TestInstallCopy_TheFixedPathIsNeverMissingOrPartial(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "app-serve", "initech")
	old, nu := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	oldBytes := make([]byte, 4<<20)
	newBytes := append([]byte(nil), oldBytes...)
	newBytes[len(newBytes)-1] = 1
	os.WriteFile(old, oldBytes, 0o755)
	os.WriteFile(nu, newBytes, 0o755)
	unsigned := func(...string) (string, error) { return "code object is not signed at all", os.ErrInvalid }
	if err := InstallCopy(unsigned, old, dst); err != nil {
		t.Fatal(err)
	}
	var bad atomic.Int32
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			// One open per look: the file a reader holds is one file, the old
			// or the new.
			got, err := os.ReadFile(dst)
			if err != nil || (!bytes.Equal(got, oldBytes) && !bytes.Equal(got, newBytes)) {
				bad.Add(1)
			}
		}
	}()
	for i := range 10 {
		src := nu
		if i%2 == 1 {
			src = old
		}
		if err := InstallCopy(unsigned, src, dst); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
	if n := bad.Load(); n != 0 {
		t.Errorf("a reader saw the fixed path missing or partial %d times", n)
	}
	if !SameBytes(dst, old) {
		t.Error("the last copy did not land")
	}
}
