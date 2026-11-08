package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// NormalizeWebExternalOrigin validates an optional, explicitly trusted public
// HTTP(S) origin. The canonical result has lowercase scheme/host, no root slash,
// and no default port. Empty means use the request's native transport origin.
// It never consults proxy headers. It is also used by web.NewServer so callers
// constructing Config directly get the same fail-closed validation as Load.
func NormalizeWebExternalOrigin(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	invalid := func() (string, error) {
		return "", fmt.Errorf("web.external_origin must be a single absolute http/https origin with a valid host and port, without credentials, path, query or fragment")
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return invalid()
	}
	u, err := url.Parse(raw)
	if err != nil {
		return invalid()
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Opaque != "" || u.User != nil || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || u.ForceQuery || u.RawQuery != "" || strings.Contains(raw, "#") {
		return invalid()
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return invalid()
	}
	if strings.HasPrefix(u.Host, "[") {
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return invalid()
		}
		host = "[" + addr.String() + "]"
	} else {
		// Restrict authority to ASCII hostname labels (including localhost).
		// URL reg-name punctuation, escapes and origin lists are not hosts.
		if len(host) > 253 {
			return invalid()
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return invalid()
			}
			for _, c := range label {
				if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
					return invalid()
				}
			}
		}
	}
	port := u.Port()
	// An explicitly empty port is not a valid configured authority.
	if strings.HasSuffix(u.Host, ":") {
		return invalid()
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return invalid()
		}
		port = strconv.Itoa(n)
		if scheme == "http" && port == "80" || scheme == "https" && port == "443" {
			port = ""
		}
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}
