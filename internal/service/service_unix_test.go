//go:build linux || darwin

package service

import (
	"slices"
	"testing"
)

func TestServeArgs(t *testing.T) {
	got := serveArgs(&Options{
		Addr:     "0.0.0.0:8080",
		DB:       "/tmp/x.db",
		Config:   "/tmp/c.toml",
		MDNS:     true,
		MDNSName: "fwrd",
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
	got := serveArgs(&Options{Addr: "127.0.0.1:8080"})
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
