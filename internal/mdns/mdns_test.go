package mdns

import (
	"net"
	"strings"
	"testing"
)

func TestIsLANCandidate(t *testing.T) {
	const lan = net.FlagUp | net.FlagBroadcast | net.FlagMulticast
	cases := []struct {
		name  string
		flags net.Flags
		want  bool
	}{
		{"en0", lan, true},
		{"en9", lan, true},
		{"eth0", lan, true},
		{"bridge100", lan, false},                             // VM bridge
		{"utun8", lan | net.FlagPointToPoint, false},          // tunnel/VPN
		{"docker0", lan, false},                               // container bridge
		{"tailscale0", lan | net.FlagPointToPoint, false},     // tailnet
		{"awdl0", lan, false},                                 // Apple wireless direct
		{"lo0", net.FlagUp | net.FlagLoopback, false},         // loopback
		{"en1", net.FlagBroadcast | net.FlagMulticast, false}, // down (no FlagUp)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ifi := net.Interface{Name: c.name, Flags: c.flags}
			if got := isLANCandidate(&ifi); got != c.want {
				t.Fatalf("isLANCandidate(%s, %v) = %v, want %v", c.name, c.flags, got, c.want)
			}
		})
	}
}

// AdvertiseOn must reject inputs with no usable IPv4 address before it touches
// the network, so these cases exercise the filter without starting a responder.
func TestAdvertiseOn_NoUsableIPv4(t *testing.T) {
	cases := map[string][]net.IP{
		"empty":     nil,
		"ipv6 only": {net.ParseIP("fe80::1"), net.ParseIP("2001:db8::1")},
	}
	for name, ips := range cases {
		t.Run(name, func(t *testing.T) {
			adv, err := AdvertiseOn("fwrd", 8080, ips)
			if err == nil {
				_ = adv.Close()
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), "no usable IPv4") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
