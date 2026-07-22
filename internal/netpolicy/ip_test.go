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
