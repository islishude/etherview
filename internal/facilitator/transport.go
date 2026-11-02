package facilitator

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type boundedRPCTransport struct{ base http.RoundTripper }

func (t boundedRPCTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	const maximum = 2 << 20
	if response.ContentLength > maximum {
		_ = response.Body.Close()
		return nil, errors.New("rpc_response_too_large")
	}
	response.Body = &boundedRPCBody{ReadCloser: response.Body, remaining: maximum}
	return response, nil
}

type boundedRPCBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedRPCBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n != 0 {
			return 0, errors.New("rpc_response_too_large")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}
func dialRPC(ctx context.Context, url string) (*ethclient.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxConnsPerHost = 16
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 8 * time.Second
	client := &http.Client{Transport: boundedRPCTransport{base: transport}, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("rpc_redirect_rejected") }}
	connection, err := rpc.DialOptions(ctx, url, rpc.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	return ethclient.NewClient(connection), nil
}
