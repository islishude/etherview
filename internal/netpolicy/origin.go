package netpolicy

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// ParseHTTPSOrigin validates an operator-configured origin without resolving DNS.
// Errors intentionally omit the supplied value, which may contain credentials.
func ParseHTTPSOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || strings.IndexFunc(raw, unicode.IsControl) >= 0 ||
		strings.ContainsAny(raw, "?#") || u == nil || u.RawPath != "" ||
		(u.Path != "" && u.Path != "/") {
		return "", errors.New("expected an absolute HTTPS origin")
	}
	return HTTPSOrigin(u)
}

// HTTPSOrigin returns an exact host/effective-port key for navigation matching.
// It grants no network access and must not be used as an SSRF exception.
func HTTPSOrigin(u *url.URL) (string, error) {
	invalid := errors.New("expected an absolute HTTPS origin")
	if u == nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return "", invalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || len(host) > 253 || strings.Contains(host, "%") {
		return "", invalid
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is6() != strings.HasPrefix(u.Host, "[") {
			return "", invalid
		}
		// Preserve mapped IPv6 addresses: browsers give them a distinct origin
		// from the corresponding IPv4 literal, even if they reach the same host.
		host = ip.String()
	} else {
		for label := range strings.SplitSeq(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", invalid
			}
			for _, ch := range label {
				if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
					return "", invalid
				}
			}
		}
		if strings.ContainsAny(u.Host, "[]") {
			return "", invalid
		}
	}
	port := u.Port()
	if port == "" {
		if strings.HasSuffix(u.Host, ":") {
			return "", invalid
		}
		port = "443"
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return "", invalid
	}
	return "https://" + net.JoinHostPort(host, strconv.FormatUint(number, 10)), nil
}
