package validation

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// FeedURLValidator provides comprehensive validation for RSS feed URLs
type FeedURLValidator struct {
	// AllowLocalhost determines if localhost URLs are permitted
	AllowLocalhost bool
	// AllowPrivateIPs determines if private IP addresses are permitted
	AllowPrivateIPs bool
	// MaxLength is the maximum allowed URL length
	MaxLength int
}

// NewFeedURLValidator creates a new validator with secure defaults
func NewFeedURLValidator() *FeedURLValidator {
	return &FeedURLValidator{
		AllowLocalhost:  false, // Secure default: block localhost
		AllowPrivateIPs: false, // Secure default: block private IPs
		MaxLength:       2048,  // Reasonable URL length limit
	}
}

// NewPermissiveFeedURLValidator creates a validator that allows local development
func NewPermissiveFeedURLValidator() *FeedURLValidator {
	return &FeedURLValidator{
		AllowLocalhost:  true,
		AllowPrivateIPs: true,
		MaxLength:       2048,
	}
}

// ValidateAndNormalize validates a feed URL and returns the normalized version
func (v *FeedURLValidator) ValidateAndNormalize(input string) (string, error) {
	input = strings.TrimSpace(input)

	// Length validation
	if input == "" {
		return "", fmt.Errorf("URL cannot be empty")
	}
	if len(input) > v.MaxLength {
		return "", fmt.Errorf("URL too long (max %d characters)", v.MaxLength)
	}

	// Basic character sanitization
	if strings.ContainsAny(input, "<>\"'`") {
		return "", fmt.Errorf("URL contains invalid characters")
	}

	// Add protocol if missing (default to HTTPS for security)
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	// Parse URL
	parsedURL, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("invalid URL format: %w", err)
	}

	// Validate scheme
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("URL must use http or https protocol")
	}

	// Validate host
	if parsedURL.Host == "" {
		return "", fmt.Errorf("URL must have a valid hostname")
	}

	// Security checks on hostname
	if err := v.validateHostSecurity(parsedURL.Host); err != nil {
		return "", err
	}

	// Additional security validations
	if err := v.validatePathSecurity(parsedURL); err != nil {
		return "", err
	}

	return parsedURL.String(), nil
}

// validateHostSecurity performs security checks on the hostname
func (v *FeedURLValidator) validateHostSecurity(host string) error {
	// Extract hostname without port
	hostname := host
	if strings.Contains(host, ":") {
		var err error
		hostname, _, err = net.SplitHostPort(host)
		if err != nil {
			return fmt.Errorf("invalid host format: %w", err)
		}
	}

	// Check for localhost if not allowed
	if !v.AllowLocalhost && isLocalhost(hostname) {
		return fmt.Errorf("localhost URLs are not permitted")
	}

	// Check for private IP addresses if not allowed
	if !v.AllowPrivateIPs {
		if ip := net.ParseIP(hostname); ip != nil && isPrivateIP(ip) {
			return fmt.Errorf("private IP addresses are not permitted")
		}
	}

	// Block suspicious hostnames
	if isSuspiciousHostname(hostname) {
		return fmt.Errorf("suspicious hostname detected")
	}

	return nil
}

// validatePathSecurity performs security checks on the URL path and query
func (v *FeedURLValidator) validatePathSecurity(parsedURL *url.URL) error {
	// Check for directory traversal attempts
	if strings.Contains(parsedURL.Path, "..") {
		return fmt.Errorf("directory traversal patterns not allowed in URL path")
	}

	// Check for suspicious query parameters
	if strings.Contains(parsedURL.RawQuery, "<script") || strings.Contains(parsedURL.RawQuery, "javascript:") {
		return fmt.Errorf("suspicious query parameters detected")
	}

	return nil
}

// isLocalhost checks if a hostname refers to localhost
func isLocalhost(hostname string) bool {
	return hostname == "localhost" ||
		hostname == "127.0.0.1" ||
		hostname == "::1" ||
		strings.HasSuffix(hostname, ".localhost")
}

// isPrivateIP checks if an IP address is in a private range
func isPrivateIP(ip net.IP) bool {
	// Private IPv4 ranges:
	// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
	// Link-local: 169.254.0.0/16
	// Loopback: 127.0.0.0/8

	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // Link-local
		"127.0.0.0/8",    // Loopback
	}

	for _, cidr := range private {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil && block.Contains(ip) {
			return true
		}
	}

	// Check IPv6 private ranges
	if ip.To4() == nil { // IPv6
		// fc00::/7 (Unique local address)
		// fe80::/10 (Link-local)
		return strings.HasPrefix(ip.String(), "fc") ||
			strings.HasPrefix(ip.String(), "fd") ||
			strings.HasPrefix(ip.String(), "fe8") ||
			strings.HasPrefix(ip.String(), "fe9") ||
			strings.HasPrefix(ip.String(), "fea") ||
			strings.HasPrefix(ip.String(), "feb")
	}

	return false
}

// isSuspiciousHostname checks for potentially malicious hostnames
func isSuspiciousHostname(hostname string) bool {
	suspicious := []string{
		"example.com",
		"test.com",
		"localhost.com", // Often used in attacks
		"0.0.0.0",
		"255.255.255.255",
	}

	hostname = strings.ToLower(hostname)
	for _, sus := range suspicious {
		if hostname == sus {
			return true
		}
	}

	// Check for hex-encoded hostnames (potential obfuscation)
	// But exclude valid IP addresses
	if len(hostname) > 8 && strings.Count(hostname, ".") == 3 {
		// First check if it's a valid IP address - if so, don't flag it
		if net.ParseIP(hostname) != nil {
			return false // It's a valid IP address, not suspicious
		}

		parts := strings.Split(hostname, ".")
		hexPattern := true
		for _, part := range parts {
			if len(part) > 6 && !isHexString(part) {
				hexPattern = false
				break
			}
		}
		if hexPattern {
			return true
		}
	}

	return false
}

// isHexString checks if a string contains only hexadecimal characters
func isHexString(s string) bool {
	for _, char := range s {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
