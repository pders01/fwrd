//go:build linux

package service

import (
	"strings"
	"testing"
)

func TestUnitContentQuotesPathsAndDisablesExpansion(t *testing.T) {
	got, err := unitContent(&Options{BinPath: "/home/user/My Apps/fwrd", Config: "/home/user/$CONFIG%/settings.toml", Addr: "127.0.0.1:8080", TLS: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"/home/user/My Apps/fwrd"`, `"/home/user/$$CONFIG%%/settings.toml"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("unit missing safe argument %s: %s", want, got)
		}
	}
	if _, err := unitContent(&Options{BinPath: "/bin/fwrd", Addr: "127.0.0.1:8080\nExecStart=/bin/other"}); err == nil {
		t.Fatal("service directive injection accepted")
	}
}

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
