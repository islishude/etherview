package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/metadata"
)

type fakeNFTMetadataReader struct {
	item    metadata.NFTMetadata
	err     error
	address common.Address
	tokenID string
}

func (reader *fakeNFTMetadataReader) NFTMetadata(
	_ context.Context,
	address common.Address,
	tokenID string,
) (metadata.NFTMetadata, error) {
	reader.address, reader.tokenID = address, tokenID
	return reader.item, reader.err
}

func nftMetadataTestHandler(t *testing.T, enabled bool, reader metadata.NFTMetadataReader) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.Chain.ID = 11155111
	cfg.Features.NFTMetadata = enabled
	handler, err := New(Options{
		Config: cfg, Reader: fakeReader{}, NFTMetadataReader: reader,
		RequestID: func() string { return "metadata-display-request" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestNFTMetadataResponseUsesBoundedGeneratedContract(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("12", 20)
	hash := common.HexToHash("0x" + strings.Repeat("34", 32))
	contentObservation := metadata.NFTMetadataObservation{BlockNumber: 12, BlockHash: hash}
	reader := &fakeNFTMetadataReader{item: metadata.NFTMetadata{
		State: metadata.StateAvailable,
		Observation: metadata.NFTMetadataObservation{
			BlockNumber: 12,
			BlockHash:   hash,
		},
		ContentObservation: &contentObservation,
		Name:               "Example NFT", Description: "Plain description",
		NameTruncated: true, DescriptionTruncated: true,
		Attributes: []metadata.NFTMetadataAttribute{
			{TraitType: "Level", Value: "9007199254740993", DisplayType: "number"},
		},
		OmittedAttributeCount: 2,
		Image: metadata.NFTMetadataImage{
			State: metadata.NFTMetadataImageAvailable, URL: "https://media.example/nft.png?token=public", SourceScheme: "https",
		},
	}}
	recorder := httptest.NewRecorder()
	nftMetadataTestHandler(t, true, reader).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+address+"/42/metadata", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.NFTMetadataResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if reader.address != common.HexToAddress(address) || reader.tokenID != "42" {
		t.Fatalf("reader address=%s tokenID=%q", reader.address.Hex(), reader.tokenID)
	}
	data := response.Data
	if data.ChainId != "11155111" || data.TokenAddress != common.HexToAddress(address).Hex() || data.TokenId != "42" ||
		data.State != gen.NFTMetadataStateAvailable || data.Observation.BlockNumber != "12" || data.Observation.BlockHash != hash.Hex() ||
		data.ContentObservation == nil || *data.ContentObservation != data.Observation || data.ContentStale {
		t.Fatalf("identity=%+v", data)
	}
	if data.Name == nil || *data.Name != "Example NFT" || data.Description == nil || *data.Description != "Plain description" ||
		!data.NameTruncated || !data.DescriptionTruncated || data.OmittedAttributeCount != 2 || len(data.Attributes) != 1 {
		t.Fatalf("display=%+v", data)
	}
	if data.Attributes[0].Value != "9007199254740993" || data.Attributes[0].DisplayType == nil || *data.Attributes[0].DisplayType != "number" {
		t.Fatalf("attribute=%+v", data.Attributes[0])
	}
	if data.Image.State != gen.NFTMetadataImageStateAvailable || data.Image.Url == nil || *data.Image.Url != "https://media.example/nft.png?token=public" ||
		data.Image.SourceScheme == nil || *data.Image.SourceScheme != gen.Https {
		t.Fatalf("image=%+v", data.Image)
	}
}

func TestNFTMetadataResponseRetainsPriorContentDuringRefresh(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("12", 20)
	latestHash := common.HexToHash("0x" + strings.Repeat("67", 32))
	contentHash := common.HexToHash("0x" + strings.Repeat("68", 32))
	contentObservation := metadata.NFTMetadataObservation{BlockNumber: 11, BlockHash: contentHash}
	reader := &fakeNFTMetadataReader{item: metadata.NFTMetadata{
		State: metadata.StatePending,
		Observation: metadata.NFTMetadataObservation{
			BlockNumber: 12, BlockHash: latestHash,
		},
		ContentObservation: &contentObservation, ContentStale: true,
		Name: "Prior NFT", Description: "Prior canonical content",
		Attributes: []metadata.NFTMetadataAttribute{},
		Image:      metadata.NFTMetadataImage{State: metadata.NFTMetadataImageMissing},
	}}
	recorder := httptest.NewRecorder()
	nftMetadataTestHandler(t, true, reader).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+address+"/42/metadata", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response gen.NFTMetadataResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	data := response.Data
	if data.State != gen.NFTMetadataStatePending || !data.ContentStale ||
		data.ContentObservation == nil || data.ContentObservation.BlockNumber != "11" ||
		data.ContentObservation.BlockHash != contentHash.Hex() || data.Name == nil || *data.Name != "Prior NFT" ||
		data.Observation.BlockNumber != "12" || data.Observation.BlockHash != latestHash.Hex() {
		t.Fatalf("stale metadata=%+v", data)
	}
}

func TestNFTMetadataTerminalStateReturnsWithoutDisplayDocument(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("12", 20)
	reader := &fakeNFTMetadataReader{item: metadata.NFTMetadata{
		State: metadata.StateUnsafe,
		Observation: metadata.NFTMetadataObservation{
			BlockNumber: 9,
			BlockHash:   common.HexToHash("0x" + strings.Repeat("56", 32)),
		},
		Attributes: []metadata.NFTMetadataAttribute{},
		Image:      metadata.NFTMetadataImage{State: metadata.NFTMetadataImageUnavailable},
	}}
	recorder := httptest.NewRecorder()
	nftMetadataTestHandler(t, true, reader).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/nfts/"+address+"/1/metadata", nil),
	)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), `"name"`) || strings.Contains(recorder.Body.String(), `"url"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNFTMetadataTypedErrors(t *testing.T) {
	t.Parallel()
	address := "0x" + strings.Repeat("12", 20)
	tests := []struct {
		name    string
		enabled bool
		reader  metadata.NFTMetadataReader
		path    string
		status  int
		code    string
	}{
		{name: "disabled", reader: &fakeNFTMetadataReader{}, path: "/api/v1/nfts/" + address + "/1/metadata", status: http.StatusServiceUnavailable, code: "nft_metadata_disabled"},
		{name: "missing reader", enabled: true, path: "/api/v1/nfts/" + address + "/1/metadata", status: http.StatusServiceUnavailable, code: "nft_metadata_disabled"},
		{name: "not found", enabled: true, reader: &fakeNFTMetadataReader{err: metadata.ErrNFTMetadataNotFound}, path: "/api/v1/nfts/" + address + "/1/metadata", status: http.StatusNotFound, code: "nft_metadata_not_found"},
		{name: "noncanonical", enabled: true, reader: &fakeNFTMetadataReader{err: metadata.ErrNFTMetadataNoncanonical}, path: "/api/v1/nfts/" + address + "/1/metadata", status: http.StatusConflict, code: "nft_metadata_noncanonical"},
		{name: "query failure", enabled: true, reader: &fakeNFTMetadataReader{err: errors.New("secret nested error")}, path: "/api/v1/nfts/" + address + "/1/metadata", status: http.StatusInternalServerError, code: "nft_metadata_query_failed"},
		{name: "invalid address", enabled: true, reader: &fakeNFTMetadataReader{}, path: "/api/v1/nfts/0x12/1/metadata", status: http.StatusBadRequest, code: "invalid_address"},
		{name: "invalid token", enabled: true, reader: &fakeNFTMetadataReader{}, path: "/api/v1/nfts/" + address + "/01/metadata", status: http.StatusBadRequest, code: "invalid_token_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			nftMetadataTestHandler(t, test.enabled, test.reader).ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, test.path, nil),
			)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) ||
				strings.Contains(recorder.Body.String(), "secret nested error") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
