// Package netbind makes fwrd reachable at http://<name>.local on the standard
// HTTP port (80) without binding a privileged port and without colliding with
// any server the host already runs on port 80.
//
// It does this at the network layer rather than in the web server:
//
//  1. A dedicated alias IP is added to the LAN interface, giving fwrd its own
//     address on the segment, distinct from the host's primary IP.
//  2. A firewall redirect (pf on macOS, nftables on Linux) rewrites that alias
//     IP's port 80 to fwrd's unprivileged port (8080) in the PREROUTING/rdr
//     path — before the kernel's socket lookup, so it works even when the host
//     already binds 0.0.0.0:80.
//  3. mDNS then advertises <name>.local pointing at the alias IP only (see
//     mdns.AdvertiseOn and `serve --mdns-ip`), so clients resolve the name to
//     the redirect target.
//
// fwrd itself keeps binding an ordinary unprivileged port; netbind needs root
// only for the alias IP and firewall rule, so it lives in the separate
// `fwrd net up`/`down` commands rather than in `serve`.
//
// The applied state is recorded under ~/.fwrd/net.json so `down` reverses the
// exact change. The binding is not reboot-persistent in this version; re-run
// `fwrd net up` after a reboot.
package netbind

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// Options describes a requested binding. Zero fields take documented defaults.
type Options struct {
	Iface   string `json:"iface"`    // LAN interface, e.g. "en0" (macOS) or "eth0" (Linux)
	AliasIP string `json:"alias_ip"` // dedicated IP to give fwrd on that interface
	Prefix  int    `json:"prefix"`   // CIDR prefix length for the alias (Linux); default 24
	Mask    string `json:"mask"`     // dotted netmask for the alias (macOS); default 255.255.255.0
	Port    int    `json:"port"`     // public port to redirect from; default 80
	ToPort  int    `json:"to_port"`  // fwrd's unprivileged port to redirect to; default 8080
}

// State is the persisted record of an applied Up, enough to reverse it and to
// report status. It embeds the Options that produced it.
type State struct {
	Options
	Backend string `json:"backend"`            // "pf" or "nftables"
	PFToken string `json:"pf_token,omitempty"` // macOS: pfctl -E enable-reference token
}

// ErrNoBinding is returned by Down/Status when no active binding is recorded.
var ErrNoBinding = errors.New("no active fwrd net binding (run `fwrd net up` first)")

// ErrUnsupported is returned on platforms without a netbind backend.
var ErrUnsupported = errors.New("fwrd net is only supported on Linux and macOS")

// Supported reports whether this platform has a netbind backend.
func Supported() bool { return supported }

// DetectIface returns the name of the up, non-loopback interface whose subnet
// contains aliasIP. The alias IP already encodes its target subnet, so this
// makes --iface derivable rather than required — and the match is exact, not a
// default-route guess that breaks on a multi-homed host.
func DetectIface(aliasIP string) (string, error) {
	ip := net.ParseIP(aliasIP)
	if ip == nil {
		return "", fmt.Errorf("--alias-ip %q is not a valid IP", aliasIP)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.Contains(ip) {
				return ifi.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no active interface has a subnet containing %s; pass --iface explicitly", aliasIP)
}

func (o *Options) applyDefaults() {
	if o.Prefix == 0 {
		o.Prefix = 24
	}
	if o.Mask == "" {
		o.Mask = "255.255.255.0"
	}
	if o.Port == 0 {
		o.Port = 80
	}
	if o.ToPort == 0 {
		o.ToPort = 8080
	}
}

func (o *Options) validate() error {
	if o.Iface == "" {
		return fmt.Errorf("--iface is required (the LAN interface, e.g. en0 or eth0)")
	}
	if ip := net.ParseIP(o.AliasIP); ip == nil || ip.To4() == nil {
		return fmt.Errorf("--alias-ip %q is not a valid IPv4 address", o.AliasIP)
	}
	if o.Port < 1 || o.Port > 65535 || o.ToPort < 1 || o.ToPort > 65535 {
		return fmt.Errorf("ports must be 1–65535 (got port=%d to-port=%d)", o.Port, o.ToPort)
	}
	return nil
}

// Up assigns the alias IP and installs the redirect, recording state so Down
// can reverse it. It requires root.
func Up(o *Options) (*State, error) {
	if !supported {
		return nil, ErrUnsupported
	}
	o.applyDefaults()
	if err := o.validate(); err != nil {
		return nil, err
	}
	if err := requireRoot(); err != nil {
		return nil, err
	}
	if existing, _ := loadState(); existing != nil {
		return nil, fmt.Errorf("a binding is already active (%s:%d → :%d on %s); run `fwrd net down` first",
			existing.AliasIP, existing.Port, existing.ToPort, existing.Iface)
	}
	st, err := applyUp(o)
	if err != nil {
		return nil, err
	}
	if err := saveState(st); err != nil {
		_ = applyDown(st) // best-effort rollback so we don't leave an untracked rule
		return nil, fmt.Errorf("recording state: %w", err)
	}
	return st, nil
}

// Down reverses the recorded binding and clears the state file. It requires
// root. Returns ErrNoBinding if nothing is recorded.
func Down() (*State, error) {
	if !supported {
		return nil, ErrUnsupported
	}
	if err := requireRoot(); err != nil {
		return nil, err
	}
	st, err := loadState()
	if err != nil {
		return nil, err
	}
	if err := applyDown(st); err != nil {
		return st, err
	}
	if err := clearState(); err != nil {
		return st, fmt.Errorf("removing state file: %w", err)
	}
	return st, nil
}

// Status returns the active binding, or ErrNoBinding if none is recorded.
func Status() (*State, error) { return loadState() }

func requireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("must run as root; try: sudo fwrd net …")
	}
	return nil
}

func statePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fwrd", "net.json"), nil
}

func saveState(s *State) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(filepath.Dir(p), 0o755); mkErr != nil {
		return mkErr
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

func loadState() (*State, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoBinding
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	return &s, nil
}

func clearState() error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
