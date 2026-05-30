// Package webtls provides TLS certificate sources for the fwrd web server.
//
// HTTPS is on by default; a certificate can come from one of three origins
// behind a single Source interface:
//
//   - self-signed: an auto-generated leaf. Zero setup, but browsers show a
//     one-time "not private" warning until the cert is trusted per device.
//   - local-ca: an auto-generated local CA plus a leaf it signs. Warning-free
//     once the CA is trusted on each device that visits.
//   - file: a user-supplied cert/key pair (bring-your-own).
//
// Generated material is persisted under a directory (default ~/.fwrd/tls) and
// reused across restarts. It is regenerated only when missing, near expiry, or
// when the requested host set no longer matches the certificate's SANs.
package webtls

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Certificate-source modes. An empty mode is treated as ModeSelfSigned.
const (
	ModeSelfSigned = "self-signed"
	ModeLocalCA    = "local-ca"
	ModeFile       = "file"
)

const (
	// leafValidity stays under the 398-day cap browsers enforce on TLS server
	// certificates.
	leafValidity = 397 * 24 * time.Hour
	// caValidity is long-lived: trusting the local CA per device is a one-time
	// step, so a short rotation would only add friction.
	caValidity = 10 * 365 * 24 * time.Hour
	// renewWindow regenerates a leaf this long before it actually expires, so a
	// long-running server never serves an expired cert.
	renewWindow = 30 * 24 * time.Hour
)

// Source yields a server TLS config and a one-line description for logging.
type Source interface {
	TLSConfig() (*tls.Config, error)
	Describe() string
}

// NewSource selects a certificate source. A non-empty certFile/keyFile pair
// forces the file source regardless of mode; otherwise mode chooses between
// the self-signed and local-CA generators (empty mode defaults to
// self-signed). hosts are the DNS names and IP literals the generated leaf
// must cover via SANs.
func NewSource(mode, dir, certFile, keyFile string, hosts []string) (Source, error) {
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("both --tls-cert and --tls-key are required for a bring-your-own certificate")
		}
		return &fileSource{certFile: certFile, keyFile: keyFile}, nil
	}
	switch mode {
	case "", ModeSelfSigned:
		return &generatedSource{dir: dir, hosts: hosts, withCA: false}, nil
	case ModeLocalCA:
		return &generatedSource{dir: dir, hosts: hosts, withCA: true}, nil
	case ModeFile:
		return nil, fmt.Errorf("tls mode %q requires --tls-cert and --tls-key", mode)
	default:
		return nil, fmt.Errorf("unknown tls mode %q (want %s, %s, or %s)", mode, ModeSelfSigned, ModeLocalCA, ModeFile)
	}
}

// fileSource serves a user-supplied keypair as-is.
type fileSource struct{ certFile, keyFile string }

func (s *fileSource) TLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading TLS keypair: %w", err)
	}
	return serverConfig(&cert), nil
}

func (s *fileSource) Describe() string { return "file (" + s.certFile + ")" }

// generatedSource load-or-generates a leaf (self-signed, or signed by a local
// CA when withCA) persisted under dir.
type generatedSource struct {
	dir    string
	hosts  []string
	withCA bool
}

func (s *generatedSource) certPath() string  { return filepath.Join(s.dir, "cert.pem") }
func (s *generatedSource) keyPath() string   { return filepath.Join(s.dir, "key.pem") }
func (s *generatedSource) caPath() string    { return filepath.Join(s.dir, "ca.pem") }
func (s *generatedSource) caKeyPath() string { return filepath.Join(s.dir, "ca-key.pem") }

func (s *generatedSource) TLSConfig() (*tls.Config, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating tls dir %s: %w", s.dir, err)
	}
	if err := s.ensure(); err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(s.certPath(), s.keyPath())
	if err != nil {
		return nil, fmt.Errorf("loading generated keypair: %w", err)
	}
	return serverConfig(&cert), nil
}

func (s *generatedSource) Describe() string {
	if s.withCA {
		return "local CA (trust " + s.caPath() + ") + leaf " + s.certPath()
	}
	return "self-signed " + s.certPath()
}

// ensure (re)generates the leaf — and the CA, in local-ca mode — when the
// on-disk leaf is missing, near expiry, or no longer covers exactly the
// requested hosts.
func (s *generatedSource) ensure() error {
	if s.leafValid() {
		return nil
	}
	var (
		caCert *x509.Certificate
		caKey  *ecdsa.PrivateKey
	)
	if s.withCA {
		var err error
		if caCert, caKey, err = s.ensureCA(); err != nil {
			return err
		}
	}
	return s.generateLeaf(caCert, caKey)
}

// leafValid reports whether the persisted leaf can be reused: it parses, its
// key is present, it is comfortably before expiry, its origin still matches the
// requested mode, and its SANs match the requested host set exactly.
func (s *generatedSource) leafValid() bool {
	leaf, err := loadCert(s.certPath())
	if err != nil {
		return false
	}
	if _, err := os.Stat(s.keyPath()); err != nil {
		return false
	}
	if time.Now().Add(renewWindow).After(leaf.NotAfter) {
		return false
	}
	// A mode switch must force regeneration. A self-signed leaf carries a
	// matching issuer and subject; a CA-signed leaf does not. If that no
	// longer matches the requested mode, the on-disk leaf is stale.
	if selfSigned := bytes.Equal(leaf.RawIssuer, leaf.RawSubject); selfSigned == s.withCA {
		return false
	}
	dns, ips := splitHosts(s.hosts)
	return sansMatch(leaf, dns, ips)
}

// ensureCA reuses a persisted, unexpired CA or mints a fresh one.
func (s *generatedSource) ensureCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if cert, err := loadCert(s.caPath()); err == nil && time.Now().Before(cert.NotAfter) {
		if key, err := loadKey(s.caKeyPath()); err == nil {
			return cert, key, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "fwrd local CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(s.caPath(), pemBlock("CERTIFICATE", der), 0o644); err != nil {
		return nil, nil, err
	}
	if err := writeKey(s.caKeyPath(), key); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// generateLeaf mints a fresh leaf for the requested hosts. With a nil caCert
// the leaf is self-signed; otherwise it is signed by the CA and written as a
// leaf+CA chain so clients are handed the CA even if they don't have it yet.
func (s *generatedSource) generateLeaf(caCert *x509.Certificate, caKey *ecdsa.PrivateKey) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	dns, ips := splitHosts(s.hosts)
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: leafCN(dns)},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(leafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	parent, signerKey := tmpl, key // self-signed by default
	if caCert != nil {
		parent, signerKey = caCert, caKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signerKey)
	if err != nil {
		return err
	}
	certPEM := pemBlock("CERTIFICATE", der)
	if caCert != nil {
		certPEM = append(certPEM, pemBlock("CERTIFICATE", caCert.Raw)...)
	}
	if err := os.WriteFile(s.certPath(), certPEM, 0o644); err != nil {
		return err
	}
	return writeKey(s.keyPath(), key)
}

func serverConfig(cert *tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	}
}

// splitHosts partitions hostnames from IP literals (deduping each), matching
// how x509 stores DNSNames vs IPAddresses.
func splitHosts(hosts []string) (dns []string, ips []net.IP) {
	seenDNS, seenIP := map[string]bool{}, map[string]bool{}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			if k := ip.String(); !seenIP[k] {
				seenIP[k] = true
				ips = append(ips, ip)
			}
			continue
		}
		if !seenDNS[h] {
			seenDNS[h] = true
			dns = append(dns, h)
		}
	}
	return dns, ips
}

// sansMatch reports whether cert's SANs equal the requested sets exactly
// (order-independent), so a changed host list forces regeneration.
func sansMatch(cert *x509.Certificate, dns []string, ips []net.IP) bool {
	have := make([]string, len(cert.IPAddresses))
	for i, ip := range cert.IPAddresses {
		have[i] = ip.String()
	}
	want := make([]string, len(ips))
	for i, ip := range ips {
		want[i] = ip.String()
	}
	return sameSet(cert.DNSNames, dns) && sameSet(have, want)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func loadCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return x509.ParseCertificate(blk.Bytes)
}

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return x509.ParseECPrivateKey(blk.Bytes)
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pemBlock("EC PRIVATE KEY", der), 0o600)
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// serial returns a random 128-bit certificate serial number.
func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(1)
	}
	return n
}

func leafCN(dns []string) string {
	if len(dns) > 0 {
		return dns[0]
	}
	return "fwrd"
}
