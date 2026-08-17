package netpolicy

import (
	"net"
	"testing"
)

func TestPublicIPRejectsSpecialPurposeNetworks(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.0.0.1", "192.0.2.1", "192.31.196.1", "192.52.193.1",
		"192.88.99.1", "192.168.0.1", "192.175.48.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "::1",
		"64:ff9b::1", "64:ff9b:1::1", "100::1", "2001::1", "2001:db8::1",
		"2002::1", "2620:4f:8000::1", "3fff::1", "5f00::1", "fc00::1", "fe80::1", "ff00::1",
	} {
		if PublicIP(net.ParseIP(raw)) {
			t.Errorf("special-purpose address %s was accepted", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if !PublicIP(net.ParseIP(raw)) {
			t.Errorf("public address %s was rejected", raw)
		}
	}
}

func TestClassifyIPExplainsPolicyWithoutChangingDecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ip             string
		allowed        bool
		classification IPClassification
		prefix         string
	}{
		{ip: "198.18.17.210", classification: IPClassificationSpecialPurpose, prefix: "198.18.0.0/15"},
		{ip: "10.0.0.1", classification: IPClassificationPrivate},
		{ip: "127.0.0.1", classification: IPClassificationLoopback},
		{ip: "169.254.169.254", classification: IPClassificationLinkLocal},
		{ip: "0.0.0.0", classification: IPClassificationUnspecified},
		{ip: "224.0.0.1", classification: IPClassificationLinkLocal},
		{ip: "239.0.0.1", classification: IPClassificationNonGlobal},
		{ip: "1.1.1.1", allowed: true, classification: IPClassificationPublic},
	}
	for _, test := range tests {
		t.Run(test.ip, func(t *testing.T) {
			ip := net.ParseIP(test.ip)
			decision := ClassifyIP(ip)
			if decision.Allowed != test.allowed || decision.Classification != test.classification ||
				decision.Prefix != test.prefix || PublicIP(ip) != test.allowed {
				t.Fatalf("ClassifyIP(%s)=%+v PublicIP=%t", test.ip, decision, PublicIP(ip))
			}
		})
	}
	invalid := ClassifyIP(net.IP{1, 2, 3})
	if invalid.Allowed || invalid.Classification != IPClassificationInvalid || invalid.Prefix != "" {
		t.Fatalf("invalid decision=%+v", invalid)
	}
}
