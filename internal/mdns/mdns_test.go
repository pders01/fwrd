package mdns

import (
	"net"
	"strings"
	"testing"
)

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
