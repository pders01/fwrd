//go:build darwin

package netbind

import "testing"

func TestRenderPFAnchor(t *testing.T) {
	o := &Options{Iface: "en0", AliasIP: "192.168.1.240", Port: 80, ToPort: 8080}
	got := renderPFAnchor(o)
	want := "rdr pass on en0 inet proto tcp from any to 192.168.1.240 port 80 -> 127.0.0.1 port 8080\n"
	if got != want {
		t.Fatalf("renderPFAnchor:\n got %q\nwant %q", got, want)
	}
}

func TestParsePFToken(t *testing.T) {
	out := "No ALTQ support in kernel\nALTQ related functions disabled\npf enabled\nToken : 12345678901234567890\n"
	if got := parsePFToken(out); got != "12345678901234567890" {
		t.Fatalf("parsePFToken = %q, want the token", got)
	}
	if got := parsePFToken("pf enabled\n"); got != "" {
		t.Fatalf("parsePFToken with no token = %q, want empty", got)
	}
}
