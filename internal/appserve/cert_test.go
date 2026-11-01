package appserve

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The service speaks TLS only, with one certificate kept across calls and
// pinned by the fingerprint the pairing link carries: a client checking
// that pin connects, a plain-HTTP request to the same port gets no answer,
// and a client pinning another fingerprint is refused.
func TestCert_ServedOverTLSAndPinnedByItsFingerprint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if _, _, err := LoadCert(dir); !errors.Is(err, ErrNoCert) {
		t.Fatalf("before the first run: %v, want ErrNoCert", err)
	}
	cert, fp, err := EnsureCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := EnsureCert(dir)
	if err != nil || fp2 != fp {
		t.Fatalf("the certificate changed on the second call: %s then %s (%v)", fp, fp2, err)
	}
	if st, err := os.Stat(filepath.Join(dir, KeyFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v, want 0600", st.Mode().Perm())
	}

	now := time.Unix(1_800_000_000, 0)
	srv := newTestServer(&fakeBackend{}, &now)
	raw, err := net.Listen("tcp", "127.0.0.1:0") // the test's own listener; Listen refuses loopback
	if err != nil {
		t.Fatal(err)
	}
	go ServeTLS(raw, srv.Handler(), cert)
	defer raw.Close()
	url := "https://" + raw.Addr().String() + "/v1/rigs"

	pinned := func(pin string) *http.Client {
		return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // the pin below is the check
			VerifyPeerCertificate: func(certs [][]byte, _ [][]*x509.Certificate) error {
				if len(certs) == 0 || Fingerprint(certs[0]) != pin {
					return fmt.Errorf("certificate does not match the pin")
				}
				return nil
			},
		}}}
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := pinned(fp).Do(req)
	if err != nil {
		t.Fatalf("pinned client: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 {
		t.Errorf("pinned request: %d, TLS %v", resp.StatusCode, resp.TLS)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("body %q", body)
	}

	req2, _ := http.NewRequest("GET", url, nil)
	req2.Header.Set("Authorization", "Bearer "+testToken)
	if _, err := pinned("another-pin").Do(req2); err == nil {
		t.Error("a client pinning another certificate connected")
	}

	old := pinned(fp)
	old.Transport.(*http.Transport).TLSClientConfig.MaxVersion = tls.VersionTLS12
	req3, _ := http.NewRequest("GET", url, nil)
	req3.Header.Set("Authorization", "Bearer "+testToken)
	if _, err := old.Do(req3); err == nil {
		t.Error("a TLS 1.2 client connected")
	}

	c, err := net.DialTimeout("tcp", raw.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "GET /v1/rigs HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\n\r\n", testToken)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, _ := io.ReadAll(c)
	if bytes.Contains(got, []byte("200 OK")) || bytes.Contains(got, []byte("[]")) {
		t.Errorf("plain HTTP was answered: %q", got)
	}
}
