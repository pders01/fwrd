//go:build darwin

package service

import (
	"encoding/xml"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReloadLaunchAgentFirstInstall(t *testing.T) {
	var calls []string
	runner := func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "bootout" {
			return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
		}
		return nil, nil
	}

	if err := reloadLaunchAgent("/tmp/fwrd.plist", 501, runner, func(time.Duration) {}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"bootout gui/501/com.fwrd.serve",
		"enable gui/501/com.fwrd.serve",
		"bootstrap gui/501 /tmp/fwrd.plist",
		"print gui/501/com.fwrd.serve",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("launchctl calls = %q, want %q", calls, want)
	}
}

func TestReloadLaunchAgentWaitsForBootout(t *testing.T) {
	var calls []string
	removalChecks, sleeps := 0, 0
	bootstrapped := false
	runner := func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "print":
			if bootstrapped {
				return nil, nil
			}
			removalChecks++
			if removalChecks < 3 {
				return nil, nil
			}
			return []byte("Could not find service"), errors.New("exit status 113")
		case "bootstrap":
			bootstrapped = true
		}
		return nil, nil
	}

	if err := reloadLaunchAgent("/tmp/fwrd.plist", 501, runner, func(time.Duration) { sleeps++ }); err != nil {
		t.Fatal(err)
	}
	if removalChecks != 3 || sleeps != 2 {
		t.Fatalf("removal checks = %d, sleeps = %d; want 3, 2", removalChecks, sleeps)
	}
	want := []string{
		"bootout gui/501/com.fwrd.serve",
		"print gui/501/com.fwrd.serve",
		"print gui/501/com.fwrd.serve",
		"print gui/501/com.fwrd.serve",
		"enable gui/501/com.fwrd.serve",
		"bootstrap gui/501 /tmp/fwrd.plist",
		"print gui/501/com.fwrd.serve",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("launchctl calls = %q, want %q", calls, want)
	}
}

func TestReloadLaunchAgentRetriesTransientBootstrap(t *testing.T) {
	attempts, sleeps := 0, 0
	runner := func(args ...string) ([]byte, error) {
		switch args[0] {
		case "bootout":
			return []byte("No such process"), errors.New("exit status 3")
		case "bootstrap":
			attempts++
			if attempts < 3 {
				return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
			}
		}
		return nil, nil
	}

	if err := reloadLaunchAgent("/tmp/fwrd.plist", 501, runner, func(time.Duration) { sleeps++ }); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || sleeps != 2 {
		t.Fatalf("bootstrap attempts = %d, sleeps = %d; want 3, 2", attempts, sleeps)
	}
}

func TestReloadLaunchAgentRejectsNonTransientBootstrapError(t *testing.T) {
	attempts := 0
	runner := func(args ...string) ([]byte, error) {
		if args[0] == "bootout" {
			return []byte("No such process"), errors.New("exit status 3")
		}
		if args[0] == "bootstrap" {
			attempts++
			return []byte("Bootstrap failed: 1: Operation not permitted"), errors.New("exit status 1")
		}
		return nil, nil
	}

	err := reloadLaunchAgent("/tmp/fwrd.plist", 501, runner, func(time.Duration) {})
	if err == nil || !strings.Contains(err.Error(), "starting launch agent") {
		t.Fatalf("reloadLaunchAgent error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("bootstrap attempts = %d, want 1", attempts)
	}
}

func TestPlistContentTLSOverrides(t *testing.T) {
	for _, tt := range tlsArgumentTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			tt.options.BinPath = "/usr/local/bin/fwrd"
			tt.options.Addr = "127.0.0.1:8080"
			got, err := plistContent(&tt.options, "/tmp/fwrd-logs")
			if err != nil {
				t.Fatal(err)
			}
			var plist struct {
				Args []string `xml:"dict>array>string"`
			}
			if err := xml.Unmarshal(got, &plist); err != nil {
				t.Fatal(err)
			}
			want := []string{tt.options.BinPath, "serve", "--addr", tt.options.Addr}
			if tt.options.Config != "" {
				want = append(want, "--config", tt.options.Config)
			}
			if tt.argument != "" {
				want = append(want, tt.argument)
			}
			if !slices.Equal(plist.Args, want) {
				t.Fatalf("ProgramArguments = %q, want %q", plist.Args, want)
			}
		})
	}
}

func TestPlistContent_WellFormedAndComplete(t *testing.T) {
	// A path with an XML metacharacter must not break the document.
	got, err := plistContent(&Options{
		BinPath:  "/Applications/fwrd & co/fwrd",
		Addr:     "0.0.0.0:8080",
		MDNS:     true,
		MDNSName: "fwrd",
	}, "/Users/test/.fwrd")
	if err != nil {
		t.Fatalf("plistContent: %v", err)
	}

	// Parses as XML → well-formed (escaping correct).
	if err := xml.Unmarshal(got, new(any)); err != nil {
		t.Fatalf("plist is not well-formed XML: %v\n%s", err, got)
	}

	s := string(got)
	for _, want := range []string{
		"<key>Label</key><string>com.fwrd.serve</string>",
		"<string>serve</string>",
		"<string>--addr</string>",
		"<string>0.0.0.0:8080</string>",
		"<string>--mdns</string>",
		"&amp;", // the ampersand in the bin path, escaped
		"serve.out.log",
		"<key>RunAtLoad</key><true/>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q in:\n%s", want, s)
		}
	}
}
