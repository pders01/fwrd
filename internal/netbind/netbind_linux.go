//go:build linux

package netbind

import (
	"fmt"
	"strconv"
)

// Linux uses iproute2 for the alias IP and nftables for the redirect. The
// redirect lives in fwrd's own table (`ip fwrd`) so teardown is a single
// `nft delete table` that never touches other firewall rules.

const supported = true

const nftTable = "fwrd"

// aliasAddArgs / aliasDelArgs build the `ip addr …` argument vectors. Split
// out as pure functions so they can be asserted in tests without root.
func aliasAddArgs(o *Options) []string {
	return []string{"addr", "add", o.AliasIP + "/" + strconv.Itoa(o.Prefix), "dev", o.Iface}
}

func aliasDelArgs(s *State) []string {
	return []string{"addr", "del", s.AliasIP + "/" + strconv.Itoa(s.Prefix), "dev", s.Iface}
}

func nftAddTableArgs() []string {
	return []string{"add", "table", "ip", nftTable}
}

func nftAddChainArgs() []string {
	// A nat prerouting chain at dstnat priority: redirect happens before the
	// socket lookup, so it intercepts the alias IP's :80 even when the host
	// already listens on 0.0.0.0:80.
	return []string{"add", "chain", "ip", nftTable, "prerouting",
		"{", "type", "nat", "hook", "prerouting", "priority", "dstnat", ";", "}"}
}

func nftAddRuleArgs(o *Options) []string {
	return []string{"add", "rule", "ip", nftTable, "prerouting",
		"ip", "daddr", o.AliasIP, "tcp", "dport", strconv.Itoa(o.Port),
		"redirect", "to", ":" + strconv.Itoa(o.ToPort)}
}

func nftDelTableArgs() []string {
	return []string{"delete", "table", "ip", nftTable}
}

func applyUp(o *Options) (*State, error) {
	if err := run("ip", aliasAddArgs(o)...); err != nil {
		return nil, fmt.Errorf("adding alias IP: %w", err)
	}
	st := &State{Options: *o, Backend: "nftables"}

	steps := [][]string{nftAddTableArgs(), nftAddChainArgs(), nftAddRuleArgs(o)}
	for _, args := range steps {
		if err := run("nft", args...); err != nil {
			// Roll back everything we just added so a partial apply leaves no trace.
			_ = run("nft", nftDelTableArgs()...)
			_ = run("ip", aliasDelArgs(st)...)
			return nil, fmt.Errorf("installing nftables redirect: %w", err)
		}
	}
	return st, nil
}

func applyDown(s *State) error {
	var firstErr error
	note := func(e error) {
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}
	if err := run("nft", nftDelTableArgs()...); err != nil {
		note(fmt.Errorf("removing nftables table: %w", err))
	}
	if err := run("ip", aliasDelArgs(s)...); err != nil {
		note(fmt.Errorf("removing alias IP: %w", err))
	}
	return firstErr
}
