//go:build linux || darwin

package service

import (
	"slices"
	"testing"
)

// The service forwards config paths, not their contents. Omission of a TLS
// argument is what lets serve honor either configured default.
func tlsArgumentTestCases() []struct {
	name     string
	options  Options
	argument string
} {
	return []struct {
		name     string
		options  Options
		argument string
	}{
		{"explicit_enable_overrides_disabled_config", Options{TLS: true, TLSExplicit: true, Config: "/tmp/tls-disabled.toml"}, "--tls=true"},
		{"explicit_disable_overrides_enabled_config", Options{TLS: false, TLSExplicit: true, Config: "/tmp/tls-enabled.toml"}, "--tls=false"},
		{"implicit_disable_retains_flag", Options{TLS: false, Config: "/tmp/tls-enabled.toml"}, "--tls=false"},
		{"default_preserves_disabled_config", Options{TLS: true, Config: "/tmp/tls-disabled.toml"}, ""},
		{"default_preserves_enabled_config", Options{TLS: true, Config: "/tmp/tls-enabled.toml"}, ""},
		{"default_without_config", Options{TLS: true}, ""},
	}
}

func TestServeArgsTLSOverrides(t *testing.T) {
	for _, tt := range tlsArgumentTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			tt.options.Addr = "127.0.0.1:8080"
			want := []string{"serve", "--addr", tt.options.Addr}
			if tt.options.Config != "" {
				want = append(want, "--config", tt.options.Config)
			}
			if tt.argument != "" {
				want = append(want, tt.argument)
			}
			if got := serveArgs(&tt.options); !slices.Equal(got, want) {
				t.Fatalf("serveArgs = %q, want %q", got, want)
			}
		})
	}
}

func TestServeArgs(t *testing.T) {
	got := serveArgs(&Options{
		Addr:     "0.0.0.0:8080",
		DB:       "/tmp/x.db",
		Config:   "/tmp/c.toml",
		MDNS:     true,
		MDNSName: "fwrd",
		TLS:      true, // default install: HTTPS on, no --tls flag emitted
	})
	want := []string{
		"serve", "--addr", "0.0.0.0:8080",
		"--config", "/tmp/c.toml",
		"--db", "/tmp/x.db",
		"--mdns", "--mdns-name", "fwrd",
	}
	if !slices.Equal(got, want) {
		t.Errorf("serveArgs:\n got %q\nwant %q", got, want)
	}
}

func TestServeArgs_MinimalOmitsOptional(t *testing.T) {
	got := serveArgs(&Options{Addr: "127.0.0.1:8080", TLS: true})
	want := []string{"serve", "--addr", "127.0.0.1:8080"}
	if !slices.Equal(got, want) {
		t.Errorf("serveArgs minimal:\n got %q\nwant %q", got, want)
	}
}

func TestServeArgs_ForwardsMDNSIPsAndIface(t *testing.T) {
	got := serveArgs(&Options{
		Addr:      "0.0.0.0:5336",
		MDNS:      true,
		MDNSName:  "fwrd",
		MDNSIPs:   []string{"192.168.1.240", "192.168.178.240"},
		MDNSIface: "en0,en9",
		TLS:       true,
	})
	want := []string{
		"serve", "--addr", "0.0.0.0:5336",
		"--mdns", "--mdns-name", "fwrd",
		"--mdns-ip", "192.168.1.240",
		"--mdns-ip", "192.168.178.240",
		"--mdns-iface", "en0,en9",
	}
	if !slices.Equal(got, want) {
		t.Errorf("serveArgs with mdns-ip/iface:\n got %q\nwant %q", got, want)
	}
}

func TestServeArgs_TLSDisabledEmitsFlag(t *testing.T) {
	got := serveArgs(&Options{Addr: "127.0.0.1:8080", TLS: false})
	want := []string{"serve", "--addr", "127.0.0.1:8080", "--tls=false"}
	if !slices.Equal(got, want) {
		t.Errorf("serveArgs tls off:\n got %q\nwant %q", got, want)
	}
}

func TestServeArgs_ForwardsTLSModeAndFiles(t *testing.T) {
	got := serveArgs(&Options{
		Addr:    "0.0.0.0:8443",
		TLS:     true,
		TLSMode: "file",
		TLSCert: "/tmp/cert.pem",
		TLSKey:  "/tmp/key.pem",
	})
	want := []string{
		"serve", "--addr", "0.0.0.0:8443",
		"--tls-mode", "file",
		"--tls-cert", "/tmp/cert.pem",
		"--tls-key", "/tmp/key.pem",
	}
	if !slices.Equal(got, want) {
		t.Errorf("serveArgs tls mode/files:\n got %q\nwant %q", got, want)
	}
}
