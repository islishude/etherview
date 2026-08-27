package x402testnet

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/jsonstrict"
)

const (
	baseSepoliaChainID      = uint64(84532)
	baseSepoliaNetwork      = "eip155:84532"
	testnetResourceMimeType = "application/json"
	testnetResourceService  = "Etherview"
	maxPreflightBody        = int64(1 << 20)
)

var preflightJSONLimits = jsonstrict.Limits{
	MaxDepth:         16,
	MaxNodes:         4096,
	SafeIntegersOnly: true,
}

type PreflightOptions struct {
	TargetURL             string
	ExpectedOperation     string
	ExpectedAccess        string
	ExpectedAsset         string
	ExpectedAssetDecimals int
	ExpectedAssetName     string
	ExpectedAssetVersion  string
	ExpectedAmountAtomic  string
	ExpectedRecipient     string
	ExpectedLedgerChainID uint64
}

// CheckServer verifies the two free, non-secret configuration surfaces before
// the payer key can be used. It never follows a redirect, uses a proxy, or
// retains a Cookie.
func CheckServer(ctx context.Context, options PreflightOptions) error {
	return checkServer(ctx, options, newPreflightHTTPClient())
}

func checkServer(
	ctx context.Context,
	options PreflightOptions,
	client *http.Client,
) error {
	origin, err := targetOrigin(options.TargetURL)
	if err != nil || options.ExpectedLedgerChainID != baseSepoliaChainID {
		return boundaryError("preflight_configuration_invalid")
	}
	var public gen.PublicConfigResponse
	if err := fetchPreflightJSON(
		ctx, client, origin+"/api/v1/config", &public,
	); err != nil {
		return err
	}
	if public.Data.ChainId != strconv.FormatUint(baseSepoliaChainID, 10) ||
		(!public.Data.Features["api_billing"] || !public.Data.Features["x402_topups"]) {
		return boundaryError("preflight_public_config_mismatch")
	}

	var billing gen.BillingConfigResponse
	if err := fetchPreflightJSON(
		ctx, client, origin+"/api/v1/billing/config", &billing,
	); err != nil {
		return err
	}
	data := billing.Data
	if !data.ApiBillingEnabled || !data.X402TopupsEnabled ||
		data.X402Version != 2 || data.Scheme != "exact" ||
		data.Network == nil || *data.Network != baseSepoliaNetwork ||
		data.Asset == nil ||
		!strings.EqualFold(string(*data.Asset), options.ExpectedAsset) ||
		data.AssetDecimals == nil ||
		*data.AssetDecimals != options.ExpectedAssetDecimals ||
		data.AssetEip712Name == nil ||
		*data.AssetEip712Name != options.ExpectedAssetName ||
		data.AssetEip712Version == nil ||
		*data.AssetEip712Version != options.ExpectedAssetVersion ||
		data.Recipient == nil ||
		!strings.EqualFold(string(*data.Recipient), options.ExpectedRecipient) {
		return boundaryError("preflight_billing_config_mismatch")
	}
	if len(data.Operations) != 1 {
		return boundaryError("preflight_billing_config_mismatch")
	}
	operation := data.Operations[0]
	if operation.Operation != options.ExpectedOperation ||
		operation.AmountAtomic != options.ExpectedAmountAtomic {
		return boundaryError("preflight_billing_config_mismatch")
	}
	return nil
}

func targetOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.Fragment != "" {
		return "", boundaryError("preflight_configuration_invalid")
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String(), nil
}

func newPreflightHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 5 * time.Second
	transport.DisableCompression = true
	transport.MaxIdleConns = 8
	transport.MaxIdleConnsPerHost = 2
	transport.MaxConnsPerHost = 4
	transport.IdleConnTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 64 << 10
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		Jar:       nil,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func fetchPreflightJSON(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	destination any,
) error {
	if client == nil {
		return boundaryError("preflight_unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return boundaryError("preflight_configuration_invalid")
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return boundaryError("preflight_unavailable")
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK ||
		len(response.Header.Values("Set-Cookie")) != 0 ||
		len(response.Header.Values("Payment-Required")) != 0 ||
		len(response.Header.Values("Payment-Response")) != 0 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return boundaryError("preflight_response_invalid")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return boundaryError("preflight_response_invalid")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPreflightBody+1))
	if err != nil || int64(len(body)) > maxPreflightBody ||
		jsonstrict.Validate(body, preflightJSONLimits) != nil {
		return boundaryError("preflight_response_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return boundaryError("preflight_response_invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return boundaryError("preflight_response_invalid")
	}
	return nil
}
