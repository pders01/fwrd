package audit

import (
	"context"
	"errors"
	"net"
	"net/url"
)

// RedactURL removes credentials, fragments and all query values from an
// absolute URL or inbound request URI. Redacting every value also protects
// provider-specific token names. Malformed input is never returned verbatim.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return "[redacted URL]"
	}
	u.User = nil
	u.Fragment = ""
	u.RawFragment = ""
	if u.RawQuery != "" {
		// Do not retain keys either: malformed queries and key-only tokens can
		// contain credentials just as values can.
		u.RawQuery = "redacted"
	}
	return u.String()
}

// SafeHTTPError retains error identity for errors.Is/As without exposing URL
// credentials or tokens embedded in net/http's error strings to logs or users.
func SafeHTTPError(err error) error {
	if err == nil {
		return nil
	}
	message := "HTTP request failed"
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		message = "HTTP request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		message = "HTTP request timed out"
	case errors.As(err, &netErr) && netErr.Timeout():
		message = "HTTP request timed out"
	}
	return &safeHTTPError{message: message, cause: err}
}

type safeHTTPError struct {
	message string
	cause   error
}

func (e *safeHTTPError) Error() string { return e.message }
func (e *safeHTTPError) Unwrap() error { return e.cause }
