// Package service installs fwrd's web view as a per-user background service:
// a systemd user unit on Linux, a launchd LaunchAgent on macOS. It is
// deliberately user-level (no root) — it manages only the invoking user's
// service and writes under their home directory.
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options describes the `fwrd serve` invocation the installed service runs.
type Options struct {
	BinPath              string   // absolute path to the fwrd binary
	Addr                 string   // --addr value, e.g. "0.0.0.0:8080"
	MDNS                 bool     // advertise over mDNS (--mdns)
	MDNSName             string   // --mdns-name label
	MDNSIPs              []string // optional --mdns-ip values (alias IPs from `fwrd net up`)
	MDNSIface            string   // optional --mdns-iface restriction
	TLS                  bool     // serve over HTTPS; emits --tls=false when disabled
	TLSExplicit          bool     // emit --tls=true when explicitly enabled, overriding config
	TLSMode              string   // optional --tls-mode (self-signed|local-ca|file)
	TLSCert              string   // optional --tls-cert path (bring-your-own)
	TLSKey               string   // optional --tls-key path (bring-your-own)
	Config               string   // optional --config path to forward
	DB                   string   // optional --db path to forward
	AllowUnauthenticated bool     // explicit opt-in to an unauthenticated LAN listener
}

// prepareOptions snapshots all paths while still in the installing shell's
// working directory. Service managers need not inherit that directory.
func prepareOptions(options *Options) (*Options, error) {
	if options == nil {
		return nil, fmt.Errorf("service options are required")
	}
	prepared := *options
	for _, path := range []*string{&prepared.BinPath, &prepared.Config, &prepared.DB, &prepared.TLSCert, &prepared.TLSKey} {
		if *path == "" || *path == ":memory:" {
			continue
		}
		if strings.ContainsAny(*path, "\x00\r\n") {
			return nil, fmt.Errorf("service path contains control characters")
		}
		if strings.HasPrefix(*path, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve home: %w", err)
			}
			*path = filepath.Join(home, (*path)[2:])
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return nil, fmt.Errorf("resolve service path: %w", err)
		}
		*path = absolute
	}
	return &prepared, nil
}
