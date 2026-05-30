package webtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func leafFromConfig(t *testing.T, s Source) *x509.Certificate {
	t.Helper()
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("want 1 certificate, got %d", len(cfg.Certificates))
	}
	leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf
}

func TestSelfSigned_CoversHostsAndPersists(t *testing.T) {
	dir := t.TempDir()
	hosts := []string{"fwrd.local", "localhost", "127.0.0.1", "192.168.1.240"}
	s, err := NewSource(ModeSelfSigned, dir, "", "", hosts)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	leaf := leafFromConfig(t, s)
	if err := leaf.VerifyHostname("fwrd.local"); err != nil {
		t.Errorf("cert should cover fwrd.local: %v", err)
	}
	if err := leaf.VerifyHostname("192.168.1.240"); err != nil {
		t.Errorf("cert should cover the alias IP: %v", err)
	}

	// Files were written and are reused on the next call (same serial).
	if _, err := os.Stat(filepath.Join(dir, "cert.pem")); err != nil {
		t.Errorf("cert.pem not persisted: %v", err)
	}
	again := leafFromConfig(t, s)
	if again.SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		t.Errorf("expected the persisted cert to be reused, got a new serial")
	}
}

func TestSelfSigned_RegeneratesWhenHostsChange(t *testing.T) {
	dir := t.TempDir()
	first, err := NewSource(ModeSelfSigned, dir, "", "", []string{"a.local"})
	if err != nil {
		t.Fatal(err)
	}
	leaf1 := leafFromConfig(t, first)

	second, err := NewSource(ModeSelfSigned, dir, "", "", []string{"a.local", "b.local"})
	if err != nil {
		t.Fatal(err)
	}
	leaf2 := leafFromConfig(t, second)

	if leaf2.SerialNumber.Cmp(leaf1.SerialNumber) == 0 {
		t.Error("expected regeneration when the host set changed")
	}
	if err := leaf2.VerifyHostname("b.local"); err != nil {
		t.Errorf("regenerated cert should cover the new host: %v", err)
	}
}

func TestLocalCA_LeafChainsToCA(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSource(ModeLocalCA, dir, "", "", []string{"fwrd.local"})
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafFromConfig(t, s)

	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatalf("ca.pem not written: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to load CA into pool")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "fwrd.local", Roots: pool}); err != nil {
		t.Errorf("leaf should chain to the local CA: %v", err)
	}
}

func TestModeSwitch_RegeneratesLeaf(t *testing.T) {
	dir := t.TempDir()
	hosts := []string{"fwrd.local"}

	// Start self-signed: leaf is its own issuer.
	ss, err := NewSource(ModeSelfSigned, dir, "", "", hosts)
	if err != nil {
		t.Fatal(err)
	}
	selfLeaf := leafFromConfig(t, ss)
	if selfLeaf.Issuer.CommonName != selfLeaf.Subject.CommonName {
		t.Fatalf("self-signed leaf should be its own issuer, got issuer=%q subject=%q",
			selfLeaf.Issuer.CommonName, selfLeaf.Subject.CommonName)
	}

	// Switch to local-ca over the same dir: the stale self-signed leaf must be
	// replaced by a CA-signed one (the bug was reusing it).
	ca, err := NewSource(ModeLocalCA, dir, "", "", hosts)
	if err != nil {
		t.Fatal(err)
	}
	caLeaf := leafFromConfig(t, ca)
	if caLeaf.Issuer.CommonName == caLeaf.Subject.CommonName {
		t.Errorf("after switch to local-ca the leaf is still self-signed (issuer=subject=%q)", caLeaf.Subject.CommonName)
	}
	if caLeaf.Issuer.CommonName != "fwrd local CA" {
		t.Errorf("leaf issuer = %q, want 'fwrd local CA'", caLeaf.Issuer.CommonName)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.pem")); err != nil {
		t.Errorf("ca.pem should exist after switching to local-ca: %v", err)
	}

	// Switch back to self-signed: leaf must become self-issued again.
	back, err := NewSource(ModeSelfSigned, dir, "", "", hosts)
	if err != nil {
		t.Fatal(err)
	}
	backLeaf := leafFromConfig(t, back)
	if backLeaf.Issuer.CommonName != backLeaf.Subject.CommonName {
		t.Errorf("after switching back to self-signed the leaf is still CA-signed (issuer=%q)", backLeaf.Issuer.CommonName)
	}
}

func TestFileSource_LoadsProvidedPair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "c.pem")
	keyPath := filepath.Join(dir, "k.pem")
	writeTestKeyPair(t, certPath, keyPath, "byo.local")

	s, err := NewSource(ModeFile, dir, certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewSource(file): %v", err)
	}
	leaf := leafFromConfig(t, s)
	if err := leaf.VerifyHostname("byo.local"); err != nil {
		t.Errorf("file source should serve the provided cert: %v", err)
	}
}

func TestNewSource_Errors(t *testing.T) {
	if _, err := NewSource(ModeFile, t.TempDir(), "", "", nil); err == nil {
		t.Error("file mode without cert/key should error")
	}
	if _, err := NewSource(ModeSelfSigned, t.TempDir(), "c.pem", "", nil); err == nil {
		t.Error("a lone --tls-cert should error")
	}
	if _, err := NewSource("bogus", t.TempDir(), "", "", nil); err == nil {
		t.Error("unknown mode should error")
	}
}

// writeTestKeyPair writes a minimal self-signed cert/key to feed the file source.
func writeTestKeyPair(t *testing.T, certPath, keyPath, host string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}
