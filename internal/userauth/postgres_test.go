package userauth

import (
	"testing"
	"time"
)

func TestNormalizePublicOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input     string
		scheme    string
		authority string
		origin    string
	}{
		{"https://EXAMPLE.com:443/", "https", "example.com", "https://example.com"},
		{"http://localhost:8080", "http", "localhost:8080", "http://localhost:8080"},
		{"https://[2001:db8::1]:8443", "https", "[2001:db8::1]:8443", "https://[2001:db8::1]:8443"},
	}
	for _, test := range tests {
		scheme, authority, origin, err := normalizePublicOrigin(test.input)
		if err != nil {
			t.Fatalf("normalize %q: %v", test.input, err)
		}
		if scheme != test.scheme || authority != test.authority || origin != test.origin {
			t.Errorf("normalize %q = %q %q %q", test.input, scheme, authority, origin)
		}
	}
	for _, invalid := range []string{
		"", "example.com", "ftp://example.com", "https://user@example.com",
		"https://example.com/path", "https://example.com/?query=1",
		"https://example.com/#fragment", "https://example.com.",
		"http://example.com", "https://example.com:",
		"https://example.com:0", "https://example.com:65536",
	} {
		if _, _, _, err := normalizePublicOrigin(invalid); err == nil {
			t.Errorf("accepted invalid public URL %q", invalid)
		}
	}
}

func TestAdministrativeInputBounds(t *testing.T) {
	t.Parallel()
	repository := &PostgresRepository{}
	if _, err := repository.Users(t.Context(), nil, 0, ""); err == nil {
		t.Fatal("accepted zero user page limit")
	}
	if _, err := repository.Users(t.Context(), &UserPageAfter{
		ID: "not-a-uuid", CreatedAt: time.Now(),
	}, 1, ""); err == nil {
		t.Fatal("accepted malformed page position")
	}
	if _, err := repository.Cleanup(t.Context(), time.Now(), 1001); err == nil {
		t.Fatal("accepted oversized cleanup batch")
	}
}
