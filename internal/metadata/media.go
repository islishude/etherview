package metadata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MediaProxy is the route-independent core of the safe media proxy. It never
// stores media and only returns byte-signature-validated formats accepted by
// Client.Fetch(KindImage). A future HTTP adapter must preserve NoStore and must
// not infer a content type from the source URL.
type MediaProxy struct {
	fetcher Fetcher
}

type ProxiedMedia struct {
	ResolvedURI string
	ContentType string
	Body        []byte
	ETag        string
	NoStore     bool
}

func NewMediaProxy(fetcher Fetcher) (*MediaProxy, error) {
	if fetcher == nil {
		return nil, errors.New("media proxy requires a safe metadata fetcher")
	}
	return &MediaProxy{fetcher: fetcher}, nil
}

func (proxy *MediaProxy) Fetch(ctx context.Context, sourceURI string) (ProxiedMedia, error) {
	if proxy == nil || proxy.fetcher == nil {
		return ProxiedMedia{}, errors.New("fetch media using nil safe proxy")
	}
	result, err := proxy.fetcher.Fetch(ctx, sourceURI, KindImage)
	if err != nil {
		return ProxiedMedia{}, err
	}
	if result.URL == "" || len(result.URL) > MaxSourceURIBytes || result.ContentType == "" || len(result.Body) == 0 {
		return ProxiedMedia{}, fetchFailure(FailureInvalid, errors.New("safe media fetcher returned incomplete output"))
	}
	contentType := strings.ToLower(result.ContentType)
	if !allowedContentType(KindImage, contentType) || !validImageSignature(contentType, result.Body) {
		return ProxiedMedia{}, fetchFailure(FailureUnsafeContent, errors.New("safe media fetcher returned unvalidated image content"))
	}
	digest := sha256.Sum256(result.Body)
	return ProxiedMedia{
		ResolvedURI: result.URL, ContentType: contentType,
		Body:    append([]byte(nil), result.Body...),
		ETag:    `"sha256-` + fmt.Sprintf("%x", digest[:]) + `-` + strconv.Itoa(len(result.Body)) + `"`,
		NoStore: true,
	}, nil
}
