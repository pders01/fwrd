// Package mdns advertises the web view on the local network over multicast
// DNS, so it is reachable at a stable <name>.local address (default
// fwrd.local) without DNS, a hosts entry, or a static IP. It is a thin
// wrapper over hashicorp/mdns.
//
// mDNS is link-local: it only works on the same LAN segment, and the
// advertised A records point at the host's LAN interface addresses, not
// loopback. Advertising therefore only makes sense when the web server binds
// a non-loopback address — a loopback-bound server would publish a name that
// resolves to an unreachable interface.
package mdns

import (
	"fmt"
	"net"

	"github.com/hashicorp/mdns"
)

// Advertiser publishes an mDNS A record for <name>.local plus an _http._tcp
// service record on a port, until Close is called.
type Advertiser struct {
	server *mdns.Server
	// Host is the resolved fully-qualified name, e.g. "fwrd.local.".
	Host string
}

// Advertise starts advertising name+".local" → this host's LAN IPv4
// addresses and an _http._tcp service on port. name is the bare label
// (e.g. "fwrd"); the advertised name is "<name>.local".
//
// It binds the mDNS multicast group on the usable interfaces. On Linux it
// coexists with a running Avahi: the multicast socket is shared, and the two
// answer for different names (the host's own name vs. ours), so there is no
// conflict over <name>.local.
func Advertise(name string, port int) (*Advertiser, error) {
	ips, err := lanIPv4s()
	if err != nil {
		return nil, fmt.Errorf("enumerating interfaces: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no up, non-loopback IPv4 interface to advertise on")
	}
	return advertise(name, port, ips)
}

// AdvertiseOn is like Advertise but publishes the A record for exactly the
// given addresses instead of every LAN interface address. Use it to pin
// <name>.local to a single dedicated alias IP (see `fwrd net`), so clients
// reach the redirect target rather than the host's primary address.
// Non-IPv4 inputs are dropped; at least one usable IPv4 must remain.
func AdvertiseOn(name string, port int, ips []net.IP) (*Advertiser, error) {
	v4 := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			v4 = append(v4, ip4)
		}
	}
	if len(v4) == 0 {
		return nil, fmt.Errorf("no usable IPv4 address to advertise on")
	}
	return advertise(name, port, v4)
}

func advertise(name string, port int, ips []net.IP) (*Advertiser, error) {
	host := name + ".local."
	svc, err := mdns.NewMDNSService(name, "_http._tcp", "local.", host, port, ips, []string{"fwrd web view"})
	if err != nil {
		return nil, fmt.Errorf("building mDNS service: %w", err)
	}
	server, err := mdns.NewServer(&mdns.Config{Zone: svc})
	if err != nil {
		return nil, fmt.Errorf("starting mDNS responder: %w", err)
	}
	return &Advertiser{server: server, Host: host}, nil
}

// Close stops the responder. Safe to call on a nil Advertiser.
func (a *Advertiser) Close() error {
	if a == nil || a.server == nil {
		return nil
	}
	return a.server.Shutdown()
}

// lanIPv4s returns the global-unicast IPv4 addresses of up, non-loopback
// interfaces. mDNS is link-local, so loopback addresses are useless here; we
// stay IPv4-only to avoid publishing link-local IPv6 (fe80::) records that
// many clients can't route to without a scope id.
func lanIPv4s() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !ip4.IsGlobalUnicast() {
				continue
			}
			ips = append(ips, ip4)
		}
	}
	return ips, nil
}
