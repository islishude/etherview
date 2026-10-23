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
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type pushRoundTrip func(*http.Request) (*http.Response, error)

func (f pushRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
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
	sender := NewWebPushSender(public, private, "mailto:operator@example.com")
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
	sender := NewWebPushSender(public, private, "mailto:operator@example.com")
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
		if conn, e := publicPushDial(context.Background(), "tcp", host); e == nil {
			_ = conn.Close()
			t.Fatalf("unsafe dial accepted")
		}
	}
	s := NewWebPushSender("", "", "")
	if s.client.CheckRedirect(&http.Request{}, nil) != http.ErrUseLastResponse {
		t.Fatal("redirects enabled")
	}
}
