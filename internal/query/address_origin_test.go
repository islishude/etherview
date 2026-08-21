package query

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestAddressOriginFoundAndKind(t *testing.T) {
	t.Parallel()
	source := common.HexToAddress("0x1111111111111111111111111111111111111111")
	hash := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	for _, test := range []struct {
		name        string
		accountType gen.AddressSummaryType
		kind        gen.AddressOriginKind
		query       string
	}{
		{name: "contract", accountType: gen.AddressSummaryTypeContract, kind: gen.ContractCreation, query: "trace.call_type IN ('CREATE', 'CREATE2')"},
		{name: "eoa", accountType: gen.AddressSummaryTypeEoa, kind: gen.Funding, query: "trace.value > 0"},
		{name: "delegated eoa", accountType: gen.AddressSummaryTypeDelegatedEoa, kind: gen.Funding, query: "trace.value > 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testDatabase(t,
				queryExpectation{contains: "FROM canonical_blocks", columns: columns(1), rows: [][]driver.Value{{true}}},
				queryExpectation{contains: "FROM genesis_account_observations", columns: columns(1), rows: [][]driver.Value{{false}}},
				queryExpectation{contains: test.query, columns: columns(originColumns(test.accountType)), rows: originRows(test.accountType, source.Bytes(), hash.Bytes())},
				queryExpectation{contains: "core_complete.complete AND trace_complete.complete", columns: columns(1), rows: [][]driver.Value{{true}}},
			)
			reader, err := NewPostgresReader(db, Options{ChainID: 1})
			if err != nil {
				t.Fatal(err)
			}
			origin, err := reader.AddressOrigin(t.Context(),
				"0x2222222222222222222222222222222222222222",
				test.accountType, 12, common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if origin.Kind != test.kind || origin.State != gen.AddressOriginStateFound ||
				origin.SourceAddress == nil || *origin.SourceAddress != source.Hex() ||
				origin.TransactionHash == nil || *origin.TransactionHash != hash.Hex() {
				t.Fatalf("origin=%+v", origin)
			}
		})
	}
}

func TestAddressOriginSupportsBlockWithdrawalAndFeeRecipient(t *testing.T) {
	t.Parallel()
	blockHash := common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	for _, test := range []struct {
		name      string
		query     string
		kind      gen.AddressOriginKind
		index     any
		wantIndex string
	}{
		{name: "withdrawal", query: "FROM withdrawals AS withdrawal", kind: gen.Withdrawal, index: "7", wantIndex: "7"},
		{name: "fee recipient", query: "FROM blocks AS block", kind: gen.BlockFeeRecipient},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testDatabase(t,
				queryExpectation{contains: "FROM canonical_blocks", columns: columns(1), rows: [][]driver.Value{{true}}},
				queryExpectation{contains: "FROM genesis_account_observations", columns: columns(1), rows: [][]driver.Value{{false}}},
				queryExpectation{contains: test.query, columns: columns(6), rows: [][]driver.Value{{"9", nil, nil, string(test.kind), blockHash.Bytes(), test.index}}},
				queryExpectation{contains: "core_complete.complete AND trace_complete.complete", columns: columns(1), rows: [][]driver.Value{{true}}},
			)
			reader, err := NewPostgresReader(db, Options{ChainID: 1})
			if err != nil {
				t.Fatal(err)
			}
			origin, err := reader.AddressOrigin(t.Context(),
				"0x2222222222222222222222222222222222222222",
				gen.AddressSummaryTypeEoa, 12, common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if origin.State != gen.AddressOriginStateFound || origin.Kind != test.kind ||
				origin.BlockHash == nil || *origin.BlockHash != blockHash.Hex() ||
				origin.BlockNumber == nil || *origin.BlockNumber != "9" ||
				origin.SourceAddress != nil || origin.TransactionHash != nil {
				t.Fatalf("origin=%+v", origin)
			}
			if test.wantIndex == "" {
				if origin.WithdrawalIndex != nil {
					t.Fatalf("fee recipient unexpectedly has withdrawal index: %+v", origin)
				}
			} else if origin.WithdrawalIndex == nil || *origin.WithdrawalIndex != test.wantIndex {
				t.Fatalf("withdrawal index=%v, want %v", origin.WithdrawalIndex, test.wantIndex)
			}
		})
	}
}

func originColumns(accountType gen.AddressSummaryType) int {
	if accountType == gen.AddressSummaryTypeContract {
		return 3
	}
	return 6
}

func originRows(accountType gen.AddressSummaryType, source, transaction []byte) [][]driver.Value {
	if accountType == gen.AddressSummaryTypeContract {
		return [][]driver.Value{{"4", source, transaction}}
	}
	return [][]driver.Value{{"4", source, transaction, "funding", common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc").Bytes(), nil}}
}

func TestAddressOriginGenesisAllocation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		accountType gen.AddressSummaryType
		kind        gen.AddressOriginKind
	}{
		{name: "eoa", accountType: gen.AddressSummaryTypeEoa, kind: gen.Funding},
		{name: "delegated eoa", accountType: gen.AddressSummaryTypeDelegatedEoa, kind: gen.Funding},
		{name: "predeploy contract", accountType: gen.AddressSummaryTypeContract, kind: gen.ContractCreation},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testDatabase(t,
				queryExpectation{contains: "FROM canonical_blocks", columns: columns(1), rows: [][]driver.Value{{true}}},
				queryExpectation{contains: "FROM genesis_account_observations", columns: columns(1), rows: [][]driver.Value{{true}}},
			)
			reader, err := NewPostgresReader(db, Options{ChainID: 1})
			if err != nil {
				t.Fatal(err)
			}
			origin, err := reader.AddressOrigin(t.Context(),
				"0x2222222222222222222222222222222222222222",
				test.accountType, 12, common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if origin.Kind != test.kind || origin.State != gen.AddressOriginStateGenesis ||
				origin.SourceAddress != nil || origin.TransactionHash != nil {
				t.Fatalf("origin=%+v", origin)
			}
		})
	}
}

func TestAddressOriginCoverageAndCanonicality(t *testing.T) {
	t.Parallel()
	t.Run("coverage unavailable", func(t *testing.T) {
		db := testDatabase(t)
		reader, err := NewPostgresReader(db, Options{ChainID: 1, StartBlock: 5})
		if err != nil {
			t.Fatal(err)
		}
		origin, err := reader.AddressOrigin(t.Context(),
			"0x2222222222222222222222222222222222222222",
			gen.AddressSummaryTypeEoa, 12, common.HexToHash("0x01"),
		)
		if err != nil || origin.State != gen.AddressOriginStateUnavailable {
			t.Fatalf("origin=%+v error=%v", origin, err)
		}
	})

	t.Run("coverage gap", func(t *testing.T) {
		db := testDatabase(t,
			queryExpectation{contains: "FROM canonical_blocks", columns: columns(1), rows: [][]driver.Value{{true}}},
			queryExpectation{contains: "FROM genesis_account_observations", columns: columns(1), rows: [][]driver.Value{{false}}},
			queryExpectation{contains: "trace.value > 0", columns: columns(3)},
			queryExpectation{contains: "core_complete.complete AND trace_complete.complete", columns: columns(1), rows: [][]driver.Value{{false}}},
		)
		reader, err := NewPostgresReader(db, Options{ChainID: 1})
		if err != nil {
			t.Fatal(err)
		}
		origin, err := reader.AddressOrigin(t.Context(),
			"0x2222222222222222222222222222222222222222",
			gen.AddressSummaryTypeEoa, 12, common.HexToHash("0x01"),
		)
		if err != nil || origin.State != gen.AddressOriginStateUnavailable {
			t.Fatalf("origin=%+v error=%v", origin, err)
		}
	})

	t.Run("complete empty history", func(t *testing.T) {
		db := testDatabase(t,
			queryExpectation{contains: "FROM canonical_blocks", columns: columns(1), rows: [][]driver.Value{{true}}},
			queryExpectation{contains: "FROM genesis_account_observations", columns: columns(1), rows: [][]driver.Value{{false}}},
			queryExpectation{contains: "trace.value > 0", columns: columns(3)},
			queryExpectation{contains: "core_complete.complete AND trace_complete.complete", columns: columns(1), rows: [][]driver.Value{{true}}},
		)
		reader, err := NewPostgresReader(db, Options{ChainID: 1})
		if err != nil {
			t.Fatal(err)
		}
		origin, err := reader.AddressOrigin(t.Context(),
			"0x2222222222222222222222222222222222222222",
			gen.AddressSummaryTypeEoa, 12, common.HexToHash("0x01"),
		)
		if err != nil || origin.State != gen.AddressOriginStateNotFound {
			t.Fatalf("origin=%+v error=%v", origin, err)
		}
	})

	t.Run("reorg", func(t *testing.T) {
		db := testDatabase(t,
			queryExpectation{contains: "FROM canonical_blocks", columns: columns(1), rows: [][]driver.Value{{false}}},
		)
		reader, err := NewPostgresReader(db, Options{ChainID: 1})
		if err != nil {
			t.Fatal(err)
		}
		_, err = reader.AddressOrigin(t.Context(),
			"0x2222222222222222222222222222222222222222",
			gen.AddressSummaryTypeContract, 12, common.HexToHash("0x01"),
		)
		if !errors.Is(err, httpapi.ErrNotReady) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestAddressOriginQueriesExcludeFailedRevertedAndZeroValueCandidates(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		"receipt.raw->>'status' = '0x1'",
		"trace.reverted = FALSE",
		"trace.canonical = TRUE",
		"inclusion.raw->>'value' <> '0x0'",
		"trace.value > 0",
		"ORDER BY block_number, tx_index, source_rank, trace_order",
	} {
		if !strings.Contains(compactSQL(dbgen.QueryFirstContractOrigin+" "+dbgen.QueryFirstFundingOrigin), compactSQL(fragment)) {
			t.Fatalf("origin queries do not enforce %q", fragment)
		}
	}
	for _, fragment := range []string{
		"FROM genesis_account_observations",
		"canonical.number = 0",
		"imported.state = 'complete'",
	} {
		if !strings.Contains(compactSQL(dbgen.QueryGenesisAddressOrigin), compactSQL(fragment)) {
			t.Fatalf("genesis origin query does not enforce %q", fragment)
		}
	}
}
