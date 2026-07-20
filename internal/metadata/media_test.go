package metadata

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestMediaProxyReturnsOnlyValidatedNoStoreBytes(t *testing.T) {
	t.Parallel()
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 1, 2}
	proxy, err := NewMediaProxy(fetcherFunc(func(_ context.Context, rawURL string, kind Kind) (Result, error) {
		if kind != KindImage {
			t.Fatalf("media fetch kind = %q", kind)
		}
		return Result{URL: rawURL, ContentType: "image/png", Body: png}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	media, err := proxy.Fetch(t.Context(), "https://media.example.invalid/42.png")
	if err != nil {
		t.Fatal(err)
	}
	if !media.NoStore || media.ContentType != "image/png" || !bytes.Equal(media.Body, png) ||
		!strings.HasPrefix(media.ETag, `"sha256-`) {
		t.Fatalf("proxied media = %+v", media)
	}
	media.Body[0] = 0
	if png[0] != 0x89 {
		t.Fatal("media proxy returned the fetcher's mutable buffer")
	}
}

func TestMediaProxyRejectsFetcherThatBypassesSignatureValidation(t *testing.T) {
	t.Parallel()
	proxy, err := NewMediaProxy(fetcherFunc(func(_ context.Context, rawURL string, _ Kind) (Result, error) {
		return Result{URL: rawURL, ContentType: "image/png", Body: []byte(`<html/>`)}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.Fetch(t.Context(), "https://media.example.invalid/not-png"); err == nil || !strings.Contains(err.Error(), "unvalidated") {
		t.Fatalf("unsafe media error = %v", err)
	}
}
