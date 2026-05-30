package netbind

import (
	"errors"
	"testing"
)

func TestApplyDefaults(t *testing.T) {
	var o Options
	o.applyDefaults()
	if o.Prefix != 24 || o.Mask != "255.255.255.0" || o.Port != 80 || o.ToPort != 8080 {
		t.Fatalf("unexpected defaults: %+v", o)
	}
	// Explicit values are preserved.
	o2 := Options{Prefix: 16, Mask: "255.255.0.0", Port: 8081, ToPort: 9090}
	o2.applyDefaults()
	if o2.Prefix != 16 || o2.Mask != "255.255.0.0" || o2.Port != 8081 || o2.ToPort != 9090 {
		t.Fatalf("defaults clobbered explicit values: %+v", o2)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		o    Options
		ok   bool
	}{
		{"valid", Options{Iface: "en0", AliasIP: "192.168.1.240", Port: 80, ToPort: 8080}, true},
		{"no iface", Options{AliasIP: "192.168.1.240", Port: 80, ToPort: 8080}, false},
		{"bad ip", Options{Iface: "en0", AliasIP: "not-an-ip", Port: 80, ToPort: 8080}, false},
		{"ipv6", Options{Iface: "en0", AliasIP: "fe80::1", Port: 80, ToPort: 8080}, false},
		{"bad port", Options{Iface: "en0", AliasIP: "192.168.1.240", Port: 0, ToPort: 8080}, false},
		{"bad to-port", Options{Iface: "en0", AliasIP: "192.168.1.240", Port: 80, ToPort: 70000}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.o.validate()
			if (err == nil) != c.ok {
				t.Fatalf("validate(%+v) err=%v, want ok=%v", c.o, err, c.ok)
			}
		})
	}
}

func TestDetectIface_Errors(t *testing.T) {
	if _, err := DetectIface("not-an-ip"); err == nil {
		t.Fatal("expected an error for an invalid IP")
	}
	// 203.0.113.0/24 is TEST-NET-3 (RFC 5737); no host is on it, so no
	// interface subnet should contain it.
	if _, err := DetectIface("203.0.113.7"); err == nil {
		t.Fatal("expected an error for an IP on no local subnet")
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := loadState(); !errors.Is(err, ErrNoBinding) {
		t.Fatalf("loadState with no file: got %v, want ErrNoBinding", err)
	}

	want := &State{
		Options: Options{Iface: "en0", AliasIP: "192.168.1.240", Prefix: 24, Mask: "255.255.255.0", Port: 80, ToPort: 8080},
		Backend: "pf",
		PFToken: "1234567890",
	}
	if err := saveState(want); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	got, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if *got != *want {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}

	if err := clearState(); err != nil {
		t.Fatalf("clearState: %v", err)
	}
	if _, err := loadState(); !errors.Is(err, ErrNoBinding) {
		t.Fatalf("loadState after clear: got %v, want ErrNoBinding", err)
	}
	// clearState is idempotent.
	if err := clearState(); err != nil {
		t.Fatalf("second clearState: %v", err)
	}
}
