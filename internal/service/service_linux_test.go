//go:build linux

package service

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestActivateSystemdUserServiceRestarts(t *testing.T) {
	t.Parallel()
	var calls []string
	runner := func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	if err := activateSystemdUserService(runner); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable fwrd.service",
		"systemctl --user restart fwrd.service",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("activation calls = %q, want %q (enable --now does not restart active units)", calls, want)
	}
}

func TestActivateSystemdUserServiceStopsOnFailure(t *testing.T) {
	t.Parallel()
	steps := []string{"daemon-reload", "enable", "restart"}
	for failAt, step := range steps {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			failure := errors.New("systemctl " + step + " failed")
			var calls []string
			err := activateSystemdUserService(func(name string, args ...string) error {
				if name != "systemctl" || len(args) < 2 || args[0] != "--user" {
					t.Fatalf("unexpected command: %s %q", name, args)
				}
				calls = append(calls, args[1])
				if len(calls)-1 == failAt {
					return failure
				}
				return nil
			})
			if !errors.Is(err, failure) {
				t.Fatalf("activation error = %v, want %v", err, failure)
			}
			if !slices.Equal(calls, steps[:failAt+1]) {
				t.Fatalf("activation calls = %q, want %q", calls, steps[:failAt+1])
			}
		})
	}
}

func TestUnitContentTLSOverrides(t *testing.T) {
	for _, tt := range tlsArgumentTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			tt.options.BinPath = "/usr/local/bin/fwrd"
			tt.options.Addr = "127.0.0.1:8080"
			got, err := unitContent(&tt.options)
			if err != nil {
				t.Fatal(err)
			}
			want := "ExecStart=/usr/local/bin/fwrd serve --addr 127.0.0.1:8080"
			if tt.options.Config != "" {
				want += " --config " + tt.options.Config
			}
			if tt.argument != "" {
				want += " " + tt.argument
			}
			var execStart string
			for _, line := range strings.Split(got, "\n") {
				if strings.HasPrefix(line, "ExecStart=") {
					execStart = line
				}
			}
			if execStart != want {
				t.Fatalf("ExecStart = %q, want %q", execStart, want)
			}
		})
	}
}

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
