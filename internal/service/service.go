// Package service installs fwrd's web view as a per-user background service:
// a systemd user unit on Linux, a launchd LaunchAgent on macOS. It is
// deliberately user-level (no root) — it manages only the invoking user's
// service and writes under their home directory.
package service

// Options describes the `fwrd serve` invocation the installed service runs.
type Options struct {
	BinPath   string   // absolute path to the fwrd binary
	Addr      string   // --addr value, e.g. "0.0.0.0:8080"
	MDNS      bool     // advertise over mDNS (--mdns)
	MDNSName  string   // --mdns-name label
	MDNSIPs   []string // optional --mdns-ip values (alias IPs from `fwrd net up`)
	MDNSIface string   // optional --mdns-iface restriction
	Config    string   // optional --config path to forward
	DB        string   // optional --db path to forward
}
