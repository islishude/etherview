// Package netpolicy contains shared hostile-network boundary checks.
package netpolicy

import (
	"net"
	"net/netip"
)

// IPClassification is a closed diagnostic category for an outbound-address
// decision. It is safe to expose in structured operational logs.
type IPClassification string

const (
	IPClassificationPublic         IPClassification = "public"
	IPClassificationInvalid        IPClassification = "invalid"
	IPClassificationPrivate        IPClassification = "private"
	IPClassificationLoopback       IPClassification = "loopback"
	IPClassificationLinkLocal      IPClassification = "link_local"
	IPClassificationUnspecified    IPClassification = "unspecified"
	IPClassificationNonGlobal      IPClassification = "non_global_unicast"
	IPClassificationSpecialPurpose IPClassification = "special_use"
)

// IPDecision is the public-network policy decision for one address. Prefix is
// populated only when a fixed IANA special-purpose exclusion matched.
type IPDecision struct {
	Allowed        bool
	Classification IPClassification
	Prefix         string
}

// PublicIP reports whether an address is safe for an outbound request that
// must not reach operator, cloud-metadata, documentation, transition, or other
// special-purpose networks.
func PublicIP(ip net.IP) bool {
	return ClassifyIP(ip).Allowed
}

// ClassifyIP applies the exact PublicIP policy while retaining a stable,
// bounded explanation for operator diagnostics.
func ClassifyIP(ip net.IP) IPDecision {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return IPDecision{Classification: IPClassificationInvalid}
	}
	address = address.Unmap()
	switch {
	case address.IsUnspecified():
		return IPDecision{Classification: IPClassificationUnspecified}
	case address.IsLoopback():
		return IPDecision{Classification: IPClassificationLoopback}
	case address.IsPrivate():
		return IPDecision{Classification: IPClassificationPrivate}
	case address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast():
		return IPDecision{Classification: IPClassificationLinkLocal}
	case !address.IsGlobalUnicast():
		return IPDecision{Classification: IPClassificationNonGlobal}
	}
	// IsGlobalUnicast deliberately includes addresses that are not public
	// Internet destinations, so apply the IANA special-purpose exclusions too.
	for _, prefix := range nonPublicSpecialPrefixes {
		if prefix.Contains(address) {
			return IPDecision{
				Classification: IPClassificationSpecialPurpose,
				Prefix:         prefix.String(),
			}
		}
	}
	return IPDecision{Allowed: true, Classification: IPClassificationPublic}
}

var nonPublicSpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}
