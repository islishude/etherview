package enrich

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

// Page the indexed event stream once and deduplicate endpoints in memory.
// Paging DISTINCT holders would repeatedly scan and sort the entire history.
// All pages share a snapshot, closed before reconciliation performs RPC calls.
func (processor *PostgresHolderProcessor) readHolderCandidates(ctx context.Context, job Job, token common.Address, full bool) ([]common.Address, error) {
	seen := make(map[common.Address]struct{})
	err := dbaccess.WithTransactionOptions(ctx, processor.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(queries *dbgen.Queries) error {
		cursor := dbgen.HolderCandidatesParams{ChainID: job.ChainID, TokenAddress: token[:], BlockNumber: strconv.FormatUint(job.BlockNumber, 10), BeforeNumber: "0", BeforeHash: make([]byte, common.HashLength), PageLimit: 512}
		for {
			var rows []dbgen.HolderCandidatesRow
			var err error
			if full {
				rows, err = queries.HolderCandidates(ctx, cursor)
			} else {
				var touched []dbgen.HolderTouchedCandidatesRow
				touched, err = queries.HolderTouchedCandidates(ctx, dbgen.HolderTouchedCandidatesParams{ChainID: cursor.ChainID, TokenAddress: cursor.TokenAddress, BlockNumber: cursor.BlockNumber, BlockHash: job.BlockHash[:], HasCursor: cursor.HasCursor, BeforeNumber: cursor.BeforeNumber, BeforeLog: cursor.BeforeLog, BeforeSub: cursor.BeforeSub, BeforeHash: cursor.BeforeHash, PageLimit: cursor.PageLimit})
				rows = make([]dbgen.HolderCandidatesRow, len(touched))
				for index, row := range touched {
					rows[index] = dbgen.HolderCandidatesRow(row)
				}
			}
			if err != nil {
				return fmt.Errorf("query holder candidates: %w", err)
			}
			for _, row := range rows {
				number, err := strconv.ParseUint(row.BlockNumber, 10, 64)
				if err != nil || number > job.BlockNumber || row.LogIndex < 0 || row.SubIndex < 0 || len(row.BlockHash) != common.HashLength {
					return Permanent(errors.New("holder event cursor is invalid"))
				}
				for _, encoded := range [][]byte{row.FromAddress, row.ToAddress} {
					if len(encoded) == 0 {
						continue
					}
					if len(encoded) != common.AddressLength {
						return Permanent(errors.New("holder candidate address has invalid length"))
					}
					address := common.BytesToAddress(encoded)
					if address != (common.Address{}) {
						seen[address] = struct{}{}
					}
				}
			}
			if len(rows) < int(cursor.PageLimit) {
				return nil
			}
			last := rows[len(rows)-1]
			cursor.HasCursor = true
			cursor.BeforeNumber = last.BlockNumber
			cursor.BeforeLog = last.LogIndex
			cursor.BeforeSub = last.SubIndex
			cursor.BeforeHash = last.BlockHash
		}
	})
	if err != nil {
		return nil, err
	}
	holders := make([]common.Address, 0, len(seen))
	for address := range seen {
		holders = append(holders, address)
	}
	slices.SortFunc(holders, func(left, right common.Address) int { return bytes.Compare(left[:], right[:]) })
	return holders, nil
}
