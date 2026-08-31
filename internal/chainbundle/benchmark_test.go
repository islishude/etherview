package chainbundle_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

var benchmarkBundleSink chainbundle.Bundle

func BenchmarkBundleBoundary(b *testing.B) {
	for _, shape := range []struct {
		name               string
		transactions       int
		logsPerTransaction int
	}{
		{name: "empty", transactions: 0, logsPerTransaction: 0},
		{name: "typical", transactions: 20, logsPerTransaction: 1},
		{name: "large", transactions: 200, logsPerTransaction: 2},
	} {
		transactionTypes := make([]uint8, shape.transactions)
		for index := range transactionTypes {
			transactionTypes[index] = types.DynamicFeeTxType
		}
		fixture, err := testfixture.New(testfixture.Options{
			Number:             1,
			ExtraData:          []byte("benchmark-" + shape.name),
			TransactionTypes:   transactionTypes,
			LogsPerTransaction: shape.logsPerTransaction,
			BaseFee:            big.NewInt(1),
		})
		if err != nil {
			b.Fatalf("build %s fixture: %v", shape.name, err)
		}
		rawBytes := len(fixture.RawBlock)
		for _, raw := range fixture.RawReceipts {
			rawBytes += len(raw)
		}
		for _, raw := range fixture.RawUncles {
			rawBytes += len(raw)
		}

		b.Run(shape.name+"/decode", func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(rawBytes), "input-bytes")
			for b.Loop() {
				decoded, decodeErr := chainbundle.DecodeBlock(fixture.RawBlock, fixture.RawUncles)
				if decodeErr != nil {
					b.Fatal(decodeErr)
				}
				decoded, decodeErr = decoded.WithReceipts(fixture.RawReceipts)
				if decodeErr != nil {
					b.Fatal(decodeErr)
				}
				benchmarkBundleSink = decoded
			}
		})
		b.Run(shape.name+"/validate", func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(rawBytes), "input-bytes")
			for b.Loop() {
				if validateErr := chainbundle.Validate(fixture); validateErr != nil {
					b.Fatal(validateErr)
				}
			}
		})
		b.Run(shape.name+"/clone", func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(rawBytes), "input-bytes")
			for b.Loop() {
				clone, cloneErr := fixture.Clone()
				if cloneErr != nil {
					b.Fatal(cloneErr)
				}
				benchmarkBundleSink = clone
			}
		})
	}
}
