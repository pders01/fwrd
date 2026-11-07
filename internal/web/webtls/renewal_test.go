package webtls

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func configLeaf(t *testing.T, cfg *tls.Config) *x509.Certificate {
	t.Helper()
	cert, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func TestGeneratedConfigRenewsAtRuntime(t *testing.T) {
	for _, withCA := range []bool{false, true} {
		name := "self-signed"
		if withCA {
			name = "local-ca"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			s := &generatedSource{dir: t.TempDir(), hosts: []string{"127.0.0.1"}, withCA: withCA, now: func() time.Time { return now }}
			cfg, err := s.TLSConfig()
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Certificates) != 0 || cfg.GetCertificate == nil || cfg.MinVersion < tls.VersionTLS12 {
				t.Fatal("generated config must renew even without SNI and retain TLS policy")
			}
			first := configLeaf(t, cfg)
			now = first.NotAfter.Add(-renewWindow - time.Second)
			if got := configLeaf(t, cfg); got.SerialNumber.Cmp(first.SerialNumber) != 0 {
				t.Fatal("renewed before renewal window")
			}
			now = first.NotAfter.Add(-renewWindow)
			second := configLeaf(t, cfg)
			if second.SerialNumber.Cmp(first.SerialNumber) == 0 || !second.NotAfter.After(first.NotAfter) {
				t.Fatal("same live TLS config did not renew at renewal threshold")
			}
			persisted, err := loadCert(s.certPath())
			if err != nil || persisted.SerialNumber.Cmp(second.SerialNumber) != 0 {
				t.Fatalf("renewed cert not persisted: %v", err)
			}

			// IP literal ServerName suppresses SNI on the wire. Verify with
			// trusted roots, rather than weakening certificate verification.
			roots := x509.NewCertPool()
			if withCA {
				ca, loadErr := loadCert(s.caPath())
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				roots.AddCert(ca)
			} else {
				roots.AddCert(second)
			}
			serverConn, clientConn := net.Pipe()
			defer serverConn.Close()
			defer clientConn.Close()
			_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
			_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
			server := tls.Server(serverConn, cfg)
			client := tls.Client(clientConn, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "127.0.0.1", Time: func() time.Time { return now }})
			serverDone := make(chan error, 1)
			go func() { serverDone <- server.Handshake() }()
			if err := client.Handshake(); err != nil {
				t.Fatalf("verified no-SNI handshake: %v", err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
			if got := client.ConnectionState().PeerCertificates[0]; got.SerialNumber.Cmp(second.SerialNumber) != 0 {
				t.Fatal("no-SNI client received stale certificate")
			}
		})
	}
}

func TestGeneratedConcurrentRenewalUsesOneCertificate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := &generatedSource{dir: t.TempDir(), hosts: []string{"localhost"}, now: func() time.Time { return now }}
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	first := configLeaf(t, cfg)
	now = first.NotAfter // also cover a server idle throughout the renewal window
	var wg sync.WaitGroup
	results := make(chan *tls.Certificate, 32)
	for range cap(results) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cert, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
			if err != nil {
				t.Error(err)
				return
			}
			results <- cert
		}()
	}
	wg.Wait()
	close(results)
	var renewed *tls.Certificate
	for cert := range results {
		if renewed == nil {
			renewed = cert
		}
		if cert != renewed || cert.Leaf.SerialNumber.Cmp(first.SerialNumber) == 0 {
			t.Fatal("concurrent handshakes did not share one renewed cert")
		}
	}
}

func TestLocalCARotatesBeforeItCanExpireUnderLeaf(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := &generatedSource{dir: t.TempDir(), hosts: []string{"localhost"}, withCA: true, now: func() time.Time { return now }}
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	oldCA, err := loadCert(s.caPath())
	if err != nil {
		t.Fatal(err)
	}
	// The next leaf would outlive the old CA, even though the CA itself
	// is not expired yet. Renewing only the leaf would leave invalid TLS.
	now = oldCA.NotAfter.Add(-leafValidity / 2)
	leaf := configLeaf(t, cfg)
	newCA, err := loadCert(s.caPath())
	if err != nil {
		t.Fatal(err)
	}
	if newCA.SerialNumber.Cmp(oldCA.SerialNumber) == 0 || leaf.NotAfter.After(newCA.NotAfter) {
		t.Fatal("renewed leaf outlives its CA or failed to rotate expiring CA")
	}
	roots := x509.NewCertPool()
	roots.AddCert(newCA)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost", CurrentTime: now}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRenewalRetainsValidCertificateOnTransientFailure(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	dir := t.TempDir()
	s := &generatedSource{dir: filepath.Join(dir, "tls"), hosts: []string{"localhost"}, now: func() time.Time { return now }}
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	first := configLeaf(t, cfg)
	now = first.NotAfter.Add(-renewWindow)
	s.dir = filepath.Join(dir, "not-a-directory")
	if writeErr := os.WriteFile(s.dir, []byte("file"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if retained := configLeaf(t, cfg); retained.SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatal("renewal failure discarded a valid certificate")
	}
	now = first.NotAfter
	if _, certErr := cfg.GetCertificate(&tls.ClientHelloInfo{}); certErr == nil {
		t.Fatal("retry backoff allowed an expired certificate")
	}
}

func TestRuntimeRenewalFailsExplicitlyWhenPersistenceFails(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	dir := t.TempDir()
	s := &generatedSource{dir: filepath.Join(dir, "tls"), hosts: []string{"localhost"}, now: func() time.Time { return now }}
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	first := configLeaf(t, cfg)
	now = first.NotAfter
	// A regular file where the source directory belongs makes persistence
	// fail deterministically, including when tests run with elevated access.
	s.dir = filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(s.dir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("served expired cached certificate after renewal failure")
	}
}
