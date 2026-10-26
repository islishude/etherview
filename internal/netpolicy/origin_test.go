package netpolicy

import "testing"

func TestParseHTTPSOrigin(t *testing.T) {
	for _, raw := range []string{"https://IPFS.localhost", "https://ipfs.localhost.:443/", "https://ipfs.localhost:0443"} {
		got, err := ParseHTTPSOrigin(raw)
		if err != nil || got != "https://ipfs.localhost:443" {
			t.Fatalf("origin %q: %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "http://ipfs.localhost", "https://*.localhost", "https://user:secret@ipfs.localhost", "https://ipfs.localhost/path", "https://ipfs.localhost?", "https://ipfs.localhost#", "https://ipfs.localhost/#fragment", "https://ipfs.localhost:0", "https://ipfs.localhost:65536", "https://ipfs.localhost:", "https://ipfs.localhost\n", "https://ipfs.localhost..", "https://[localhost]", "https://[fe80::1%25en0]", "https://ipfs.localhost/%2f"} {
		if _, err := ParseHTTPSOrigin(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://127.0.0.1", "https://[::1]:8443", "https://192.168.1.1"} {
		if _, err := ParseHTTPSOrigin(raw); err != nil {
			t.Fatalf("explicit IP origin: %v", err)
		}
	}
}

func TestHTTPSOriginsKeepIPFamiliesDistinct(t *testing.T) {
	ipv4, err := ParseHTTPSOrigin("https://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := ParseHTTPSOrigin("https://[::ffff:127.0.0.1]")
	if err != nil {
		t.Fatal(err)
	}
	if ipv4 == mapped {
		t.Fatal("distinct browser origins collapsed into one allowlist key")
	}
	alternate, err := ParseHTTPSOrigin("https://[0:0:0:0:0:ffff:7f00:1]:443")
	if err != nil || alternate != mapped {
		t.Fatalf("equivalent IPv6 spellings differ: %q %q %v", alternate, mapped, err)
	}
	if _, err := ParseHTTPSOrigin("https://[127.0.0.1]"); err == nil {
		t.Fatal("accepted invalid bracketed IPv4 host")
	}
}
