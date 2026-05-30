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
	"slices"
	"strings"

	"github.com/hashicorp/mdns"
)

// virtualPrefixes are interface-name prefixes for tunnels, VM/container
// bridges, and Apple internal links that are not real LANs we want to publish
// <name>.local on. Used by the AdvertiseAll auto-selection; an explicit
// interface list bypasses it.
var virtualPrefixes = []string{
	"awdl", "llw", "utun", "gif", "stf", "anpi", "ap", // darwin internal / tunnels
	"bridge", "vmnet", "vnic", // darwin VM bridges
	"docker", "veth", "virbr", "br-", "vnet", "wg", "tailscale", // linux container/VPN
}

// isLANCandidate reports whether an interface should be auto-advertised on:
// up, multicast-capable, not loopback or point-to-point, and not a known
// virtual/tunnel device.
func isLANCandidate(ifi *net.Interface) bool {
	if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
		return false
	}
	if ifi.Flags&net.FlagPointToPoint != 0 || ifi.Flags&net.FlagMulticast == 0 {
		return false
	}
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(ifi.Name, p) {
			return false
		}
	}
	return true
}

// Advertiser publishes an mDNS A record for <name>.local plus an _http._tcp
// service record on a port, until Close is called. It may run more than one
// underlying responder (see AdvertiseAll).
type Advertiser struct {
	servers []*mdns.Server
	// Host is the resolved fully-qualified name, e.g. "fwrd.local.".
	Host string
	// Targets lists the advertised addresses (e.g. "en0=192.168.1.5") for
	// logging.
	Targets []string
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
	return advertise(name, port, ips, ipsToStrings(ips))
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
	return advertise(name, port, v4, ipsToStrings(v4))
}

// AdvertiseAll runs one responder per usable interface, each bound to its
// interface (mdns.Config.Iface) and advertising only that interface's IPv4
// address(es). On a multi-homed host this means a query is answered with the
// address reachable on the subnet it arrived from, so <name>.local resolves
// correctly on every LAN the host is on — instead of a single responder that
// might hand a client an address on a subnet it cannot route to.
//
// If only is non-empty, advertising is restricted to exactly those interface
// names (bypassing the virtual/tunnel filter); otherwise every LAN-candidate
// interface is used (see isLANCandidate).
func AdvertiseAll(name string, port int, only []string) (*Advertiser, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerating interfaces: %w", err)
	}
	host := name + ".local."
	adv := &Advertiser{Host: host}
	for i := range ifaces {
		ifi := ifaces[i]
		if len(only) > 0 {
			if !slices.Contains(only, ifi.Name) {
				continue
			}
		} else if !isLANCandidate(&ifi) {
			continue
		}
		ips := ifaceIPv4s(&ifi)
		if len(ips) == 0 {
			continue
		}
		svc, err := mdns.NewMDNSService(name, "_http._tcp", "local.", host, port, ips, []string{"fwrd web view"})
		if err != nil {
			_ = adv.Close()
			return nil, fmt.Errorf("building mDNS service for %s: %w", ifi.Name, err)
		}
		srv, err := mdns.NewServer(&mdns.Config{Zone: svc, Iface: &ifi})
		if err != nil {
			_ = adv.Close()
			return nil, fmt.Errorf("starting mDNS responder on %s: %w", ifi.Name, err)
		}
		adv.servers = append(adv.servers, srv)
		for _, ip := range ips {
			adv.Targets = append(adv.Targets, ifi.Name+"="+ip.String())
		}
	}
	if len(adv.servers) == 0 {
		if len(only) > 0 {
			return nil, fmt.Errorf("none of the requested interfaces %v are up with an IPv4 address", only)
		}
		return nil, fmt.Errorf("no up, non-loopback IPv4 interface to advertise on")
	}
	return adv, nil
}

func advertise(name string, port int, ips []net.IP, targets []string) (*Advertiser, error) {
	host := name + ".local."
	svc, err := mdns.NewMDNSService(name, "_http._tcp", "local.", host, port, ips, []string{"fwrd web view"})
	if err != nil {
		return nil, fmt.Errorf("building mDNS service: %w", err)
	}
	server, err := mdns.NewServer(&mdns.Config{Zone: svc})
	if err != nil {
		return nil, fmt.Errorf("starting mDNS responder: %w", err)
	}
	return &Advertiser{servers: []*mdns.Server{server}, Host: host, Targets: targets}, nil
}

// Close stops every underlying responder. Safe to call on a nil Advertiser.
func (a *Advertiser) Close() error {
	if a == nil {
		return nil
	}
	var firstErr error
	for _, s := range a.servers {
		if s == nil {
			continue
		}
		if err := s.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func ipsToStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

// ifaceIPv4s returns the global-unicast IPv4 addresses of a single interface.
// mDNS is link-local, so we stay IPv4-only to avoid publishing link-local
// IPv6 (fe80::) records that many clients can't route to without a scope id.
func ifaceIPv4s(ifi *net.Interface) []net.IP {
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
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
	return ips
}

// lanIPv4s returns the global-unicast IPv4 addresses of up, non-loopback
// interfaces.
func lanIPv4s() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for i := range ifaces {
		ifi := ifaces[i]
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		ips = append(ips, ifaceIPv4s(&ifi)...)
	}
	return ips, nil
}
