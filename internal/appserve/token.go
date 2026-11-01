package appserve

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TokenFile is the file under the service's state folder that holds the
// SHA-256 of the one device token, in hex. The token itself is never stored:
// it is printed once, when issued.
const TokenFile = "token.sha256"

// IssueToken makes a new device token, stores its digest in dir (0600, the
// folder 0700) and returns the token. The new digest replaces any earlier
// one, so pairing a device again revokes the device paired before.
func IssueToken(dir string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(token))
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(hex.EncodeToString(sum[:]) + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, TokenFile)); err != nil {
		return "", err
	}
	return token, nil
}

// ErrNoToken is LoadDigest's answer when no token has been issued.
var ErrNoToken = errors.New("no device token issued; run initech app-serve token")

// LoadDigest reads the stored token digest from dir.
func LoadDigest(dir string) ([32]byte, error) {
	var d [32]byte
	raw, err := os.ReadFile(filepath.Join(dir, TokenFile))
	if errors.Is(err, os.ErrNotExist) {
		return d, ErrNoToken
	}
	if err != nil {
		return d, err
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(b) != len(d) {
		return d, fmt.Errorf("%s is not a token digest", filepath.Join(dir, TokenFile))
	}
	copy(d[:], b)
	return d, nil
}

// tokenMatches reports whether the request's Authorization header carries
// the token whose digest is want, comparing digests in constant time. An
// empty or malformed header never matches.
func tokenMatches(header string, want [32]byte) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// PairURL is the pairing payload the token command shows as text and QR:
// initech://pair?host=<ip>&port=<n>&token=<t>&fp=<certificate pin>. The app
// trusts the service only when its certificate's Fingerprint equals fp.
func PairURL(host string, port int, token, fp string) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("initech://pair?host=%s&port=%d&token=%s&fp=%s", host, port, token, fp)
}
