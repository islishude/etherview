package app

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/adapters"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/etherscan"
	"github.com/islishude/etherview/internal/metadata"
)

func newAPITraceCache(ctx context.Context, cfg config.Config) (accelerator.BlobStore, error) {
	if cfg.Adapters.S3Endpoint == "" {
		return nil, nil
	}
	return accelerator.NewS3BlobStore(ctx, cfg.Adapters.S3Endpoint, accelerator.S3Options{
		Bucket: cfg.Adapters.S3Bucket, Prefix: cfg.Adapters.S3Prefix, Region: cfg.Adapters.S3Region,
		AccessKey: cfg.Adapters.S3AccessKey, SecretKey: cfg.Adapters.S3SecretKey,
		SessionToken: cfg.Adapters.S3SessionToken, PathStyle: cfg.Adapters.S3PathStyle,
		OperationTimeout: cfg.Adapters.OperationTimeout, MaxObjectBytes: cfg.Adapters.S3MaxObjectBytes,
	})
}

func newAPIPriceProvider(db *sql.DB, cfg config.Config) (etherscan.PriceProvider, error) {
	if !cfg.Features.Pricing {
		return nil, nil
	}
	adapterClient, err := metadata.New(metadata.Policy{
		Timeout: cfg.Adapters.FetchTimeout, MaxBytes: int64(cfg.Adapters.MaxResponseBytes),
		MaxRedirects: cfg.Adapters.MaxRedirects, UserAgent: "etherview-adapters/1",
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("configure external adapters: %w", err)
	}
	priceService, err := adapters.NewPostgresPriceService(
		db, cfg.Chain.ID, adapterClient, adapters.PriceOptions{
			BaseURL: cfg.Adapters.PriceBaseURL, Freshness: cfg.Adapters.PriceFreshness,
			FailureTTL: cfg.Adapters.FailureTTL,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("configure price adapter: %w", err)
	}
	return func(callbackCtx context.Context) (etherscan.NativePrice, error) {
		price, quoteErr := priceService.NativePrice(callbackCtx)
		return etherscan.NativePrice{
			USD: price.USD, BTC: price.BTC, ObservedAt: price.ObservedAt,
		}, quoteErr
	}, nil
}
