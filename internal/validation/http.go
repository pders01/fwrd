package validation

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// HTTPPolicy enforces outbound address restrictions at the point of dialing.
// The zero value is strict. Permissive mode is only for explicit test/dev use.
type HTTPPolicy struct {
	permissive atomic.Bool
}

// SetPermissive explicitly permits local/private destinations for development.
func (p *HTTPPolicy) SetPermissive(allow bool) { p.permissive.Store(allow) }

// NewHTTPClient returns a client whose redirects and connections share p.
// Proxies are deliberately disabled: an HTTP proxy resolves target names itself
// and would bypass local address validation. Connections are not pooled across
// requests, so switching from permissive to strict cannot reuse a private peer.
func (p *HTTPPolicy) NewHTTPClient(timeout time.Duration) *http.Client {
	// Construct rather than clone the mutable global DefaultTransport: host
	// customizations (especially DialTLS or Proxy) must not bypass this policy.
	t := &http.Transport{
		DialContext:           p.dialContext,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &policyTransport{base: t, policy: p},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return p.validateURL(req.URL)
		},
	}
}

// NewHTTPClient creates a strict outbound client (including standalone plugins).
func NewHTTPClient(timeout time.Duration) *http.Client {
	return new(HTTPPolicy).NewHTTPClient(timeout)
}

type policyTransport struct {
	base   *http.Transport
	policy *HTTPPolicy
}

func (t *policyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.policy.validateURL(req.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

func (t *policyTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

func (p *HTTPPolicy) validateURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return errors.New("invalid HTTP destination")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if strings.Contains(host, "%") {
		return errors.New("scoped IP destinations are not permitted")
	}
	if !p.permissive.Load() {
		if isLocalhost(host) {
			return errors.New("local destinations are not permitted")
		}
		if ip := net.ParseIP(host); ip != nil && isPrivateIP(ip) {
			return errors.New("non-public destinations are not permitted")
		}
	}
	return nil
}

func (p *HTTPPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return p.dial(ctx, network, address, net.DefaultResolver.LookupIPAddr, new(net.Dialer).DialContext)
}

// Resolve once, reject the entire answer set if any address is unsafe, and dial
// only those exact IPs. Never pass the hostname back to a second DNS lookup.
func (p *HTTPPolicy) dial(ctx context.Context, network, address string,
	lookup func(context.Context, string) ([]net.IPAddr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid HTTP destination")
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if !p.permissive.Load() && isLocalhost(host) {
		return nil, errors.New("local destinations are not permitted")
	}
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		ips, err = lookup(ctx, host)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("HTTP destination lookup failed")
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("HTTP destination has no addresses")
	}
	for _, ip := range ips {
		if ip.Zone != "" || ip.IP == nil || (!p.permissive.Load() && isPrivateIP(ip.IP)) {
			return nil, errors.New("non-public destinations are not permitted")
		}
	}
	for _, ip := range ips {
		conn, dialErr := dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("HTTP connection failed")
}
