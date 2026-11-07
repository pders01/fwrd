package validation

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPPolicyRejectsNonPublicAddresses(t *testing.T) {
	addresses := []string{
		"127.0.0.1", "127.9.8.7", "10.0.0.1", "172.16.0.1", "192.168.0.1",
		"169.254.169.254", "0.0.0.0", "224.0.0.1", "255.255.255.255", "100.64.0.1",
		"::", "::1", "fc00::1", "fdff::1", "fe80::1", "ff02::1", "::ffff:127.0.0.1",
	}
	for _, address := range addresses {
		t.Run(address, func(t *testing.T) {
			p := new(HTTPPolicy)
			u := "http://" + net.JoinHostPort(address, "80") + "/feed"
			client := p.NewHTTPClient(time.Second)
			if resp, err := client.Get(u); err == nil {
				resp.Body.Close()
				t.Fatal("strict client accepted a non-public address")
			}
			if _, err := NewFeedURLValidator().ValidateAndNormalize(u); err == nil {
				t.Fatal("URL validator accepted a non-public address")
			}
		})
	}
}

func TestHTTPPolicyChecksAllAnswersAndPinsDial(t *testing.T) {
	for _, answers := range [][]string{
		{"8.8.8.8", "127.0.0.1"},
		{"::1", "2001:4860:4860::8888"},
		{"8.8.8.8", "2001:4860:4860::8888"},
	} {
		t.Run(strings.Join(answers, ","), func(t *testing.T) {
			lookups, dials := 0, 0
			lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
				lookups++
				if host != "feed.example.org" || lookups != 1 {
					t.Fatal("unexpected/repeated DNS lookup")
				}
				var ips []net.IPAddr
				for _, answer := range answers {
					ips = append(ips, net.IPAddr{IP: net.ParseIP(answer)})
				}
				return ips, nil
			}
			dial := func(_ context.Context, _, address string) (net.Conn, error) {
				dials++
				host, port, err := net.SplitHostPort(address)
				if err != nil || net.ParseIP(host) == nil || port != "443" {
					t.Fatalf("dial was not pinned to a literal IP: %s", address)
				}
				return nil, errors.New("synthetic dial failure")
			}
			_, err := new(HTTPPolicy).dial(context.Background(), "tcp", "feed.example.org:443", lookup, dial)
			if err == nil || lookups != 1 {
				t.Fatalf("err=%v lookups=%d", err, lookups)
			}
			want := 0
			if len(answers) == 2 && answers[1] == "2001:4860:4860::8888" && answers[0] == "8.8.8.8" {
				want = 2
			}
			if dials != want {
				t.Fatalf("dials=%d want %d", dials, want)
			}
		})
	}
}

func TestHTTPPolicyPermissiveIsExplicitAndReversible(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	p := new(HTTPPolicy)
	client := p.NewHTTPClient(time.Second)
	for _, permissive := range []bool{false, true, false} {
		p.SetPermissive(permissive)
		resp, err := client.Get(server.URL)
		if resp != nil {
			resp.Body.Close()
		}
		if (err == nil) != permissive {
			t.Fatalf("permissive=%t err=%v", permissive, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("server received %d requests", calls.Load())
	}
}

type redirectTransport func(*http.Request) (*http.Response, error)

func (f redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPPolicyRedirectsAndProxy(t *testing.T) {
	for _, destination := range []string{"http://127.0.0.1/private", "http://[::1]/private", "file:///etc/passwd", "http://localhost./private"} {
		t.Run(destination, func(t *testing.T) {
			client := NewHTTPClient(time.Second)
			transport := client.Transport.(*policyTransport).base
			if transport.Proxy != nil {
				t.Fatal("proxies can bypass target DNS validation")
			}
			calls := 0
			client.Transport = redirectTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {destination}}, Body: http.NoBody, Request: r}, nil
			})
			resp, err := client.Get("https://feed.example.org/")
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil || calls != 1 {
				t.Fatalf("redirect escaped policy: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestHTTPPolicyHostnameAndScopedIPv6(t *testing.T) {
	for _, host := range []string{"LOCALHOST", "localhost.", "x.LOCALHOST.", "[fe80::1%25en0]"} {
		resp, err := NewHTTPClient(time.Second).Get("http://" + host + "/")
		if resp != nil {
			resp.Body.Close()
		}
		if err == nil {
			t.Fatalf("accepted %s", host)
		}
	}
}
