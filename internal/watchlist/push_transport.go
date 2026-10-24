package watchlist

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/islishude/etherview/internal/netpolicy"
)

// PushSender never retries. A failed or ambiguous request is deliberately lost.
type PushSender interface {
	Send(context.Context, string, string, string, []byte) (int, error)
}
type WebPushSender struct {
	publicKey, privateKey, contact string
	client                         *http.Client
}

func NewWebPushSender(publicKey, privateKey, contact string, unsafeAllowPrivateNetworks bool) *WebPushSender {
	transport := &http.Transport{Proxy: nil, MaxConnsPerHost: 8, MaxIdleConns: 16, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 8192, DisableKeepAlives: true}
	transport.DialContext = pushDialer(unsafeAllowPrivateNetworks, net.DefaultResolver.LookupIPAddr, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
	// webpush-go adds the mailto scheme itself for email contacts.
	return &WebPushSender{publicKey: publicKey, privateKey: privateKey, contact: strings.TrimPrefix(contact, "mailto:"), client: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func pushDialer(unsafeAllowPrivateNetworks bool, lookup func(context.Context, string) ([]net.IPAddr, error), dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || port != "443" {
			return nil, ErrInvalid
		}
		ips, e := lookup(ctx, host)
		if e != nil || len(ips) == 0 || len(ips) > 32 {
			return nil, ErrUnavailable
		}
		for _, ip := range ips {
			if ip.Zone != "" || (!unsafeAllowPrivateNetworks && !netpolicy.PublicIP(ip.IP)) {
				return nil, ErrInvalid
			}
		}
		// Dial the checked IP, while net/http retains the original TLS server name.
		return dial(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}
func (s *WebPushSender) Send(ctx context.Context, endpoint, p256dh, auth string, payload []byte) (int, error) {
	if !validEndpoint(endpoint) || len(payload) > 2048 {
		return 0, ErrInvalid
	}
	response, e := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: endpoint, Keys: webpush.Keys{P256dh: p256dh, Auth: auth}}, &webpush.Options{HTTPClient: s.client, Subscriber: s.contact, VAPIDPublicKey: s.publicKey, VAPIDPrivateKey: s.privateKey, TTL: 0})
	if e != nil {
		return 0, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	n, e := io.Copy(io.Discard, io.LimitReader(response.Body, 4097))
	if e != nil || n > 4096 {
		return response.StatusCode, errors.New("push response unavailable")
	}
	return response.StatusCode, nil
}
