//go:build linux

package service

import (
	"strings"
	"testing"
)

func TestUnitContent(t *testing.T) {
	got, err := unitContent(&Options{
		BinPath:  "/usr/local/bin/fwrd",
		Addr:     "0.0.0.0:8080",
		MDNS:     true,
		MDNSName: "fwrd",
	})
	if err != nil {
		t.Fatalf("unitContent: %v", err)
	}
	wantExec := "ExecStart=/usr/local/bin/fwrd serve --addr 0.0.0.0:8080 --mdns --mdns-name fwrd"
	if !strings.Contains(got, wantExec) {
		t.Errorf("unit missing ExecStart line %q in:\n%s", wantExec, got)
	}
	for _, want := range []string{"[Unit]", "[Service]", "Restart=on-failure", "StartLimitBurst=5", "WantedBy=default.target"} {
		if !strings.Contains(got, want) {
			t.Errorf("unit missing %q", want)
		}
	}
}
