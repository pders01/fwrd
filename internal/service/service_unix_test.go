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
