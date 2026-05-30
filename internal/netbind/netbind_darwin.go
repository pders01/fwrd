//go:build darwin

package netbind

import (
	"fmt"
	"os/exec"
	"strings"
)

// macOS uses ifconfig for the alias IP and pf for the redirect. Rather than
// edit /etc/pf.conf, the redirect is loaded into the sub-anchor
// "com.apple/fwrd": the stock pf.conf already declares `rdr-anchor
// "com.apple/*"`, whose wildcard evaluates every sub-anchor under com.apple/.
// So loading our rule there is enough for pf to apply it — no system file is
// touched, and teardown is a flush of just our sub-anchor. (This is the same
// hook Docker and various VPNs have historically used.)

const supported = true

// pfSubAnchor is evaluated by the stock `rdr-anchor "com.apple/*"`.
const pfSubAnchor = "com.apple/fwrd"

func aliasAddArgs(o *Options) []string {
	return []string{o.Iface, "alias", o.AliasIP, o.Mask}
}

func aliasDelArgs(s *State) []string {
	return []string{s.Iface, "-alias", s.AliasIP}
}

// output runs a command and returns combined stdout+stderr, for callers that
// need to parse a result (e.g. pfctl -E's enable-reference token).
func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// renderPFAnchor is the rdr rule loaded into the fwrd sub-anchor: redirect the
// alias IP's public port to fwrd's unprivileged port on the loopback (a
// 0.0.0.0-bound server receives it). Pure so it can be asserted in tests.
func renderPFAnchor(o *Options) string {
	return fmt.Sprintf("rdr pass on %s inet proto tcp from any to %s port %d -> 127.0.0.1 port %d\n",
		o.Iface, o.AliasIP, o.Port, o.ToPort)
}

func applyUp(o *Options) (*State, error) {
	if err := run("ifconfig", aliasAddArgs(o)...); err != nil {
		return nil, fmt.Errorf("adding alias IP: %w", err)
	}
	st := &State{Options: *o, Backend: "pf"}
	if err := installPFRedirect(o, st); err != nil {
		_ = run("ifconfig", aliasDelArgs(st)...) // roll back the alias
		return nil, err
	}
	return st, nil
}

// installPFRedirect loads the redirect into the com.apple/fwrd sub-anchor and
// enables pf, recording the enable token on st. It first verifies the stock
// wildcard rdr-anchor is present, since without it the sub-anchor would load
// but never be evaluated.
func installPFRedirect(o *Options, st *State) error {
	if err := ensureAppleRdrAnchor(); err != nil {
		return err
	}
	if err := pfLoadAnchor(renderPFAnchor(o)); err != nil {
		return err
	}
	tok, err := output("pfctl", "-E")
	if err != nil {
		_ = run("pfctl", "-a", pfSubAnchor, "-F", "all") // unload what we just added
		return fmt.Errorf("enabling pf: %w", err)
	}
	st.PFToken = parsePFToken(tok)
	return nil
}

// ensureAppleRdrAnchor checks that the loaded ruleset references the
// `com.apple/*` rdr-anchor that evaluates our sub-anchor. A Mac with a
// gutted /etc/pf.conf would otherwise load our rule into a dormant anchor.
func ensureAppleRdrAnchor() error {
	out, err := output("pfctl", "-sn")
	if err != nil {
		return fmt.Errorf("reading pf ruleset: %w", err)
	}
	if !strings.Contains(out, "com.apple/*") {
		return fmt.Errorf("this Mac's pf ruleset has no `rdr-anchor \"com.apple/*\"`, so fwrd's " +
			"redirect anchor would never be evaluated; restore the stock /etc/pf.conf and reload it " +
			"(sudo pfctl -f /etc/pf.conf)")
	}
	return nil
}

// pfLoadAnchor loads rules into the com.apple/fwrd sub-anchor via stdin.
func pfLoadAnchor(rules string) error {
	cmd := exec.Command("pfctl", "-a", pfSubAnchor, "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("loading pf anchor %s: %w: %s", pfSubAnchor, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func applyDown(s *State) error {
	var firstErr error
	note := func(e error) {
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}
	// Remove our sub-anchor's rules (leaves every other com.apple sub-anchor
	// and the main ruleset untouched).
	if err := run("pfctl", "-a", pfSubAnchor, "-F", "all"); err != nil {
		note(fmt.Errorf("flushing pf anchor: %w", err))
	}
	// Drop our enable reference; -X token is precise, -d a coarse fallback.
	if s.PFToken != "" {
		_ = run("pfctl", "-X", s.PFToken)
	} else {
		_ = run("pfctl", "-d")
	}
	if err := run("ifconfig", aliasDelArgs(s)...); err != nil {
		note(fmt.Errorf("removing alias IP: %w", err))
	}
	return firstErr
}

// parsePFToken extracts the enable-reference token from `pfctl -E` output,
// whose relevant line reads "Token : 1234567890".
func parsePFToken(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if i := strings.Index(line, "Token"); i >= 0 {
			if c := strings.Index(line[i:], ":"); c >= 0 {
				return strings.TrimSpace(line[i+c+1:])
			}
		}
	}
	return ""
}
