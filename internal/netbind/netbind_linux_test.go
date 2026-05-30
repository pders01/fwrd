//go:build linux

package netbind

import (
	"strings"
	"testing"
)

func TestAliasArgs(t *testing.T) {
	o := &Options{Iface: "eth0", AliasIP: "192.168.1.240", Prefix: 24}
	if got := strings.Join(aliasAddArgs(o), " "); got != "addr add 192.168.1.240/24 dev eth0" {
		t.Fatalf("aliasAddArgs = %q", got)
	}
	s := &State{Options: *o}
	if got := strings.Join(aliasDelArgs(s), " "); got != "addr del 192.168.1.240/24 dev eth0" {
		t.Fatalf("aliasDelArgs = %q", got)
	}
}

func TestNftArgs(t *testing.T) {
	o := &Options{Iface: "eth0", AliasIP: "192.168.1.240", Port: 80, ToPort: 8080}
	if got := strings.Join(nftAddTableArgs(), " "); got != "add table ip fwrd" {
		t.Fatalf("nftAddTableArgs = %q", got)
	}
	if got := strings.Join(nftAddChainArgs(), " "); got != "add chain ip fwrd prerouting { type nat hook prerouting priority dstnat ; }" {
		t.Fatalf("nftAddChainArgs = %q", got)
	}
	if got := strings.Join(nftAddRuleArgs(o), " "); got != "add rule ip fwrd prerouting ip daddr 192.168.1.240 tcp dport 80 redirect to :8080" {
		t.Fatalf("nftAddRuleArgs = %q", got)
	}
	if got := strings.Join(nftDelTableArgs(), " "); got != "delete table ip fwrd" {
		t.Fatalf("nftDelTableArgs = %q", got)
	}
}
