package watchlist

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type pushRoundTrip func(*http.Request) (*http.Response, error)

func (f pushRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPushPrivateNetworkDialBoundary(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		for _, tc := range []struct {
			name, host string
			ips        []string
			public     bool
		}{
			{"fake IP", "fcm.googleapis.com:443", []string{"198.18.1.49"}, false},
			{"public", "other.example:443", []string{"8.8.8.8"}, true},
			{"literal", "198.18.1.49:443", []string{"198.18.1.49"}, false},
			{"loopback", "localhost:443", []string{"127.0.0.1"}, false},
			{"IPv6 loopback", "localhost:443", []string{"::1"}, false},
			{"private", "push.local:443", []string{"10.0.0.1"}, false},
			{"IPv6 private", "push.local:443", []string{"fd00::1"}, false},
			{"link local", "push.local:443", []string{"169.254.169.254"}, false},
			{"mixed answers", "push.local:443", []string{"8.8.8.8", "192.168.1.1"}, false},
		} {
			t.Run(fmt.Sprintf("%s/unsafe=%t", tc.name, unsafe), func(t *testing.T) {
				calls := 0
				lookup := func(context.Context, string) ([]net.IPAddr, error) {
					ips := make([]net.IPAddr, 0, len(tc.ips))
					for _, ip := range tc.ips {
						ips = append(ips, net.IPAddr{IP: net.ParseIP(ip)})
					}
					return ips, nil
				}
				dial := func(_ context.Context, _, address string) (net.Conn, error) {
					calls++
					if address != net.JoinHostPort(tc.ips[0], "443") {
						t.Fatalf("did not pin checked IP: %s", address)
					}
					return nil, nil
				}
				_, err := pushDialer(unsafe, lookup, dial)(t.Context(), "tcp", tc.host)
				allowed := unsafe || tc.public
				if (err == nil) != allowed || (calls == 1) != allowed {
					t.Fatalf("allow=%v calls=%d error=%v", allowed, calls, err)
				}
			})
		}
		for _, tc := range []struct {
			host string
			ips  []net.IPAddr
		}{
			{"push.example:8443", []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}},
			{"push.example:443", nil},
			{"push.example:443", make([]net.IPAddr, 33)},
			{"push.example:443", []net.IPAddr{{IP: net.ParseIP("fe80::1"), Zone: "lo0"}}},
		} {
			_, err := pushDialer(unsafe, func(context.Context, string) ([]net.IPAddr, error) { return tc.ips, nil },
				func(context.Context, string, string) (net.Conn, error) {
					t.Fatal("unsafe dial admitted")
					return nil, nil
				})(t.Context(), "tcp", tc.host)
			if err == nil {
				t.Fatal("invalid port or DNS result accepted")
			}
		}
	}
	// The default must resolve each new connection and reject private rebinding.
	lookups, dials := 0, 0
	connect := pushDialer(false, func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		ip := "8.8.8.8"
		if lookups > 1 {
			ip = "10.0.0.1"
		}
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}, func(context.Context, string, string) (net.Conn, error) { dials++; return nil, nil })
	if _, err := connect(t.Context(), "tcp", "push.example:443"); err != nil {
		t.Fatal(err)
	}
	if _, err := connect(t.Context(), "tcp", "push.example:443"); err == nil || dials != 1 {
		t.Fatal("DNS rebinding admitted by default")
	}
}

func TestPushPrivateNetworkExceptionStillRejectsRedirects(t *testing.T) {
	sender := NewWebPushSender("", "", "", true)
	transport := sender.client.Transport.(*http.Transport)
	if transport.Proxy != nil || !transport.DisableKeepAlives || transport.TLSClientConfig != nil {
		t.Fatal("changed proxy, reuse or TLS boundary")
	}
	calls := 0
	sender.client.Transport = pushRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://127.0.0.1/"}}, Body: http.NoBody}, nil
	})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://fcm.googleapis.com/send", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := sender.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if calls != 1 || response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatal("redirect followed")
	}
}
func TestWebPushEncryptionAndOneShotTransport(t *testing.T) {
	private, public, e := webpush.GenerateVAPIDKeys()
	if e != nil {
		t.Fatal(e)
	}
	browser, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	auth := bytes.Repeat([]byte{42}, 16)
	payload := []byte(`{"title":"activity","path":"/tx/0x01"}`)
	sender := NewWebPushSender(public, private, "mailto:operator@example.com", false)
	calls := 0
	sender.client.Transport = pushRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("TTL") != "0" || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("Authorization") == "" {
			t.Fatalf("missing Web Push headers")
		}
		assertVAPID(t, r.Header.Get("Authorization"), public)
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(body, payload) {
			t.Fatal("plaintext push payload")
		}
		salt, keyID := body[:16], body[21:21+int(body[20])]
		peer, e := ecdh.P256().NewPublicKey(keyID)
		if e != nil {
			t.Fatal(e)
		}
		shared, e := browser.ECDH(peer)
		if e != nil {
			t.Fatal(e)
		}
		info := append([]byte("WebPush: info\x00"), browser.PublicKey().Bytes()...)
		info = append(info, keyID...)
		ikm, e := hkdf.Key(sha256.New, shared, auth, string(info), 32)
		if e != nil {
			t.Fatal(e)
		}
		key, e := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
		if e != nil {
			t.Fatal(e)
		}
		nonce, e := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
		if e != nil {
			t.Fatal(e)
		}
		block, e := aes.NewCipher(key)
		if e != nil {
			t.Fatal(e)
		}
		gcm, e := cipher.NewGCM(block)
		if e != nil {
			t.Fatal(e)
		}
		plaintext, e := gcm.Open(nil, nonce, body[21+len(keyID):], nil)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(bytes.TrimRight(plaintext, "\x00"), append(payload, 2)) {
			t.Fatalf("decrypted payload mismatch")
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: http.Header{}}, nil
	})
	status, e := sender.Send(t.Context(), "https://push.example/send", base64.RawURLEncoding.EncodeToString(browser.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth), payload)
	if e != nil || status != 503 || calls != 1 {
		t.Fatalf("status=%d calls=%d err=%v", status, calls, e)
	}
}

func assertVAPID(t *testing.T, authorization, public string) {
	t.Helper()
	token, key, ok := strings.Cut(strings.TrimPrefix(authorization, "vapid t="), ", k=")
	if !ok || key != public {
		t.Fatal("invalid VAPID authorization")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid VAPID JWT")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(public)
	verificationKey, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
	if err != nil {
		t.Fatal(err)
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(signature) != 64 || !ecdsa.Verify(verificationKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Fatal("invalid VAPID signature")
	}
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Audience string `json:"aud"`
		Subject  string `json:"sub"`
		Expires  int64  `json:"exp"`
	}
	if json.Unmarshal(claimsJSON, &claims) != nil || claims.Audience != "https://push.example" || claims.Subject != "mailto:operator@example.com" || claims.Expires <= time.Now().Unix() || claims.Expires > time.Now().Add(24*time.Hour).Unix() {
		t.Fatal("invalid VAPID claims")
	}
}

func TestPushResponseAndTimeBounds(t *testing.T) {
	private, public, e := webpush.GenerateVAPIDKeys()
	if e != nil {
		t.Fatal(e)
	}
	key, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	p256dh := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	sender := NewWebPushSender(public, private, "mailto:operator@example.com", false)
	if sender.client.Timeout <= 0 || sender.client.Timeout > 5*time.Second {
		t.Fatal("unbounded timeout")
	}
	sender.client.Transport = pushRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 4097))), Header: http.Header{}}, nil
	})
	if _, e = sender.Send(t.Context(), "https://push.example/send", p256dh, auth, []byte("{}")); e == nil {
		t.Fatal("accepted oversized response")
	}
	calls := 0
	sender.client.Transport = pushRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, e = sender.Send(ctx, "https://push.example/send", p256dh, auth, []byte("{}")); e == nil || calls != 1 {
		t.Fatal("timeout must fail once")
	}
}
func TestPushNetworkBoundary(t *testing.T) {
	for _, endpoint := range []string{"http://push.example/", "https://user:secret@push.example/", "https://push.example:8443/", "https://push.example/#fragment"} {
		if validEndpoint(endpoint) {
			t.Fatalf("accepted unsafe endpoint")
		}
	}
	for _, host := range []string{"127.0.0.1:443", "[::1]:443", "10.0.0.1:443", "169.254.169.254:443", "8.8.8.8:80"} {
		if conn, e := pushDialer(false, net.DefaultResolver.LookupIPAddr, (&net.Dialer{}).DialContext)(context.Background(), "tcp", host); e == nil {
			_ = conn.Close()
			t.Fatalf("unsafe dial accepted")
		}
	}
	s := NewWebPushSender("", "", "", false)
	if s.client.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("redirects enabled")
	}
}
