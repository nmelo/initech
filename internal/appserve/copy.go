package appserve

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// macOS keys a privacy grant (the Desktop folder) to a program that is not
// an app bundle by its real path. A Homebrew build's real path names its
// version, so every upgrade would be a new program to macOS and ask again.
// The launchd job therefore runs a copy at one fixed path, and only the file
// at that path changes.

// CopyPath is the fixed path of the copy the launchd job runs.
func CopyPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "initech", "app-serve", "initech")
}

// Codesign runs codesign with args and answers its combined output.
type Codesign func(args ...string) (string, error)

// SystemCodesign is /usr/bin/codesign (codesign through PATH elsewhere).
func SystemCodesign(args ...string) (string, error) {
	name := "/usr/bin/codesign"
	if _, err := os.Stat(name); err != nil {
		name = "codesign"
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// Signature is what codesign reads from a binary: unsigned, or signed with an
// identifier and a team ("not set" for an Apple platform binary).
type Signature struct {
	Signed     bool
	Identifier string
	Team       string
}

// ReadSignature is path's signature. A file codesign calls not signed is
// unsigned, and so is any file where no codesign exists (a system without
// one has no signature to keep); any other failure is an error.
func ReadSignature(codesign Codesign, path string) (Signature, error) {
	out, err := codesign("-dv", "--verbose=2", path)
	if err != nil {
		if strings.Contains(out, "not signed at all") || errors.Is(err, exec.ErrNotFound) {
			return Signature{}, nil
		}
		return Signature{}, fmt.Errorf("read the signature of %s: %v: %s", path, err, strings.TrimSpace(out))
	}
	sig := Signature{Signed: true}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "Identifier="); ok {
			sig.Identifier = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "TeamIdentifier="); ok {
			sig.Team = strings.TrimSpace(v)
		}
	}
	if sig.Identifier == "" {
		return Signature{}, fmt.Errorf("read the signature of %s: codesign named no identifier", path)
	}
	return sig, nil
}

// fileSum is path's size and SHA-256.
func fileSum(path string) (int64, [32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return n, sum, nil
}

// SameBytes reports whether a and b hold the same bytes; a file that cannot
// be read is not the same.
func SameBytes(a, b string) bool {
	na, sa, err := fileSum(a)
	if err != nil {
		return false
	}
	nb, sb, err := fileSum(b)
	return err == nil && na == nb && bytes.Equal(sa[:], sb[:])
}

// VerifyCopy checks dst is a faithful copy of src: the same size and bytes,
// and, when src is signed, a signature that passes codesign --verify --strict
// with src's identifier and team.
func VerifyCopy(codesign Codesign, src, dst string) error {
	ns, ss, err := fileSum(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	nd, sd, err := fileSum(dst)
	if err != nil {
		return fmt.Errorf("read the copy: %w", err)
	}
	if ns != nd || ss != sd {
		return fmt.Errorf("the copy is not the same as %s (%d bytes, copy %d)", src, ns, nd)
	}
	want, err := ReadSignature(codesign, src)
	if err != nil {
		return err
	}
	if !want.Signed {
		return nil
	}
	if out, err := codesign("--verify", "--strict", dst); err != nil {
		return fmt.Errorf("the copy's signature does not verify: %v: %s", err, strings.TrimSpace(out))
	}
	got, err := ReadSignature(codesign, dst)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the copy is signed %s (team %s), not %s (team %s) as %s is", got.Identifier, got.Team, want.Identifier, want.Team, src)
	}
	return nil
}

// copyLockWait is how long install and the app service's watcher wait for a
// copy another process is putting in place.
const copyLockWait = 10 * time.Second

// InstallCopy puts a verified copy of src at dst: written beside it under a
// temporary name, mode 0755, checked by VerifyCopy, then renamed over dst, so
// dst is always the old file or the new one, never missing or half written.
// The folder is made 0700. Writers take the folder's copy lock, waiting up to
// copyLockWait. On any failure dst is left as it was.
func InstallCopy(codesign Codesign, src, dst string) error {
	return installCopy(codesign, src, dst, copyLockWait)
}

// installCopy is InstallCopy with the lock wait given (0: one try).
func installCopy(codesign Codesign, src, dst string, wait time.Duration) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("make %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("make %s private: %w", dir, err)
	}
	unlock, err := lockCopyDir(dir, wait)
	if err != nil {
		return fmt.Errorf("lock %s: %w", dir, err)
	}
	defer unlock()
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".initech-*")
	if err != nil {
		return fmt.Errorf("write the copy: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename; removed on failure
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("write the copy: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return fmt.Errorf("write the copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the copy: %w", err)
	}
	if err := VerifyCopy(codesign, src, tmp.Name()); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("put the copy at %s: %w", dst, err)
	}
	return nil
}
