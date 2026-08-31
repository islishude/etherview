//go:build integration

package integration_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

var (
	benchmarkCoverageSink    store.CoreCoverage
	benchmarkBlockPageSink   int
	benchmarkTransactionSink int
)

func BenchmarkCorePersistenceAndProjection(b *testing.B) {
	for _, shape := range []struct {
		name               string
		transactions       int
		logsPerTransaction int
	}{
		{name: "typical", transactions: 20, logsPerTransaction: 1},
		{name: "large", transactions: 200, logsPerTransaction: 2},
	} {
		b.Run(shape.name, func(b *testing.B) {
			db := newMigratedPostgres(b)
			repository, err := store.NewPostgresRepository(db)
			if err != nil {
				b.Fatal(err)
			}
			bundle := benchmarkCoreBundle(b, shape.transactions, shape.logsPerTransaction)
			if err := store.BindChainIdentity(b.Context(), db, "1", bundle.Block.Hash()); err != nil {
				b.Fatal(err)
			}
			if err := repository.ConfigureIndex(b.Context(), "1", 0); err != nil {
				b.Fatal(err)
			}
			coverage, err := repository.CommitCanonicalSegment(
				b.Context(), "1", []chainbundle.Bundle{bundle},
			)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkCoverageSink = coverage
			reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
			if err != nil {
				b.Fatal(err)
			}
			rawBytes := benchmarkBundleInputBytes(bundle)

			b.Run("idempotent_core_commit", func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(rawBytes), "input-bytes")
				for b.Loop() {
					coverage, commitErr := repository.CommitCanonicalSegment(
						b.Context(), "1", []chainbundle.Bundle{bundle},
					)
					if commitErr != nil {
						b.Fatal(commitErr)
					}
					benchmarkCoverageSink = coverage
				}
			})
			b.Run("block_page", func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(rawBytes), "stored-block-bytes")
				for b.Loop() {
					items, _, readErr := reader.Blocks(b.Context(), "", 25)
					if readErr != nil {
						b.Fatal(readErr)
					}
					benchmarkBlockPageSink = len(items)
				}
			})
			b.Run("transaction_page", func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(rawBytes), "stored-block-bytes")
				for b.Loop() {
					items, _, readErr := reader.Transactions(b.Context(), "", 100)
					if readErr != nil {
						b.Fatal(readErr)
					}
					benchmarkTransactionSink = len(items)
				}
			})
		})
	}
}

func benchmarkCoreBundle(b *testing.B, transactionCount, logsPerTransaction int) chainbundle.Bundle {
	b.Helper()
	transactionTypes := make([]uint8, transactionCount)
	for index := range transactionTypes {
		transactionTypes[index] = types.DynamicFeeTxType
	}
	bundle, err := testfixture.New(testfixture.Options{
		Number: 0, ExtraData: []byte("core-performance"),
		TransactionTypes: transactionTypes, LogsPerTransaction: logsPerTransaction,
		BaseFee: big.NewInt(1),
	})
	if err != nil {
		b.Fatal(err)
	}
	return bundle
}

func benchmarkBundleInputBytes(bundle chainbundle.Bundle) int {
	total := len(bundle.RawBlock)
	for _, raw := range bundle.RawReceipts {
		total += len(raw)
	}
	for _, raw := range bundle.RawUncles {
		total += len(raw)
	}
	return total
}
