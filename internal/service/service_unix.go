//go:build linux || darwin

package service

// serveArgs builds the `fwrd serve …` argument vector (excluding the binary
// itself) from the options, in a stable order so the generated unit/plist is
// deterministic.
func serveArgs(o *Options) []string {
	args := []string{"serve", "--addr", o.Addr}
	if o.Config != "" {
		args = append(args, "--config", o.Config)
	}
	if o.DB != "" {
		args = append(args, "--db", o.DB)
	}
	if o.MDNS {
		args = append(args, "--mdns", "--mdns-name", o.MDNSName)
		for _, ip := range o.MDNSIPs {
			args = append(args, "--mdns-ip", ip)
		}
		if o.MDNSIface != "" {
			args = append(args, "--mdns-iface", o.MDNSIface)
		}
	}
	return args
}
