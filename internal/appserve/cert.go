package appserve

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Files under the state folder holding the service's TLS identity. The app
// pins the certificate by its fingerprint, which travels in the pairing
// link, so no certificate authority is involved.
const (
	CertFile = "cert.pem"
	KeyFile  = "key.pem" // mode 0600
)

// ErrNoCert is LoadCert's answer before the token command has made the
// service's certificate.
var ErrNoCert = errors.New("no app-serve certificate; run initech app-serve token")

// EnsureCert is the service's certificate in dir, made on the first call
// (a P-256 key, self-signed, ten years) and the same one after: re-pairing a
// device does not change what the app pins. fp is its fingerprint.
func EnsureCert(dir string) (cert tls.Certificate, fp string, err error) {
	if c, f, err := LoadCert(dir); err == nil {
		return c, f, nil
	} else if !errors.Is(err, ErrNoCert) {
		return tls.Certificate{}, "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "initech app-serve"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writePrivate(filepath.Join(dir, KeyFile), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writePrivate(filepath.Join(dir, CertFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return tls.Certificate{}, "", err
	}
	return LoadCert(dir)
}

// LoadCert reads the service's certificate and key from dir.
func LoadCert(dir string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, "", ErrNoCert
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return c, Fingerprint(c.Certificate[0]), nil
}

// Fingerprint is a certificate's pin as the pairing link carries it: the
// SHA-256 of its DER, base64url without padding.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// TLSConfig is the server side of the pinned connection: TLS 1.3 only.
func TLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
}

// ServeTLS serves h on ln over TLS 1.3 with cert until ln closes: the only
// way the service answers, so no request is ever read in clear.
func ServeTLS(ln net.Listener, h http.Handler, cert tls.Certificate) error {
	return ServeTLSUntil(ln, h, cert, nil, 0)
}

// ServeTLSUntil is ServeTLS that, once drain closes, takes no new connection,
// lets the requests in flight finish for at most grace, then ends them and
// returns http.ErrServerClosed. A nil drain never closes. A stream that
// outlives grace (the feed) is cut, and its subscriber reconnects. When the
// listener ends on its own (closed because the address went away), the open
// connections end with it, so a later listener on the address that returns
// serves alone.
func ServeTLSUntil(ln net.Listener, h http.Handler, cert tls.Certificate, drain <-chan struct{}, grace time.Duration) error {
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- hs.Serve(tls.NewListener(ln, TLSConfig(cert))) }()
	select {
	case err := <-done:
		hs.Close() //nolint:errcheck // the listener is gone: end what it had open
		return err
	case <-drain:
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if hs.Shutdown(ctx) != nil {
		hs.Close() //nolint:errcheck // past grace: cut what is left
	}
	<-done
	return http.ErrServerClosed
}

// writePrivate writes data to path through a 0600 temp file and a rename.
func writePrivate(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
