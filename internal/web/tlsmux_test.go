package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

// testTLSConfig builds a server tls.Config with an in-memory self-signed cert
// covering 127.0.0.1, so the mux can be exercised end to end.
func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
}

// serveMux starts an http.Server over the TLS mux that reports whether each
// request arrived over TLS, and returns its base address.
func serveMux(t *testing.T) (addr string, stop func()) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newTLSMux(raw, testTLSConfig(t))
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil {
				http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusPermanentRedirect)
				return
			}
			_, _ = io.WriteString(w, "secure")
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	return raw.Addr().String(), func() { _ = srv.Close() }
}

func TestTLSMux_ServesHTTPSAndRedirectsCleartext(t *testing.T) {
	addr, stop := serveMux(t)
	defer stop()

	t.Run("https handshake succeeds", func(t *testing.T) {
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}
		resp, err := client.Get("https://" + addr + "/")
		if err != nil {
			t.Fatalf("https GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("want 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "secure" {
			t.Errorf("want body %q, got %q", "secure", body)
		}
	})

	t.Run("cleartext is redirected to https", func(t *testing.T) {
		// A client that does not follow redirects, so we observe the 308.
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get("http://" + addr + "/feeds")
		if err != nil {
			t.Fatalf("http GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusPermanentRedirect {
			t.Fatalf("want 308, got %d", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "https://"+addr+"/feeds" {
			t.Errorf("unexpected redirect target %q", loc)
		}
	})
}
