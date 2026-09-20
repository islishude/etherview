package etherscan

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (b *PostgresBackend) beaconWithdrawals(ctx context.Context, values url.Values) ([]beaconWithdrawal, error) {
	address, err := optionalAddressBytes(values.Get("address"), "address")
	if err != nil {
		return nil, err
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	start, end, err := decimalRangePolicy(values, false)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginCanonicalSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer dbaccess.Rollback(ctx, tx)
	if _, err := b.requireCanonicalCoreRange(ctx, tx, start, end); err != nil {
		return nil, err
	}
	endArgument := end
	rows, err := func() ([]dbgen.EtherscanBeaconWithdrawalsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(b.chain); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(start); err != nil {
			return nil, err
		}
		var queryValue2 pgtype.Numeric
		if endArgument != nil {
			if err := queryValue2.Scan(*endArgument); err != nil {
				return nil, err
			}
		}
		if page.limit < -2147483648 || page.limit > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		if page.offset < -2147483648 || page.offset > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).EtherscanBeaconWithdrawals(ctx, dbgen.EtherscanBeaconWithdrawalsParams{ChainID: queryValue0, Address: address, MinBlockNumber: queryValue1, MaxBlockNumber: queryValue2, Limit: int32(page.limit), Offset: int32(page.offset), SortOrder: page.direction})
	}()
	if err != nil {
		return nil, fmt.Errorf("query beacon withdrawals: %w", err)
	}

	result := make([]beaconWithdrawal, 0, page.limit)
	for _, storedRow := range rows {
		var item beaconWithdrawal
		var addressBytes []byte
		{
			item.WithdrawalIndex = storedRow.WithdrawalWithdrawalIndex
			item.ValidatorIndex = storedRow.WithdrawalValidatorIndex
			addressBytes = storedRow.Address
			item.Amount = storedRow.WithdrawalAmount
			item.BlockNumber = storedRow.WithdrawalBlockNumber
			item.Timestamp = storedRow.BlockTimestamp
		}
		for name, value := range map[string]string{
			"withdrawal index":  item.WithdrawalIndex,
			"validator index":   item.ValidatorIndex,
			"withdrawal amount": item.Amount,
			"block number":      item.BlockNumber,
			"block timestamp":   item.Timestamp,
		} {
			if _, err := storedUint256(value, name); err != nil {
				return nil, err
			}
		}
		parsed, err := addressFromBytes(addressBytes)
		if err != nil {
			return nil, err
		}
		item.Address, err = checksumAddress(parsed)
		if err != nil {
			return nil, fmt.Errorf("checksum withdrawal address: %w", err)
		}
		result = append(result, item)
	}

	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit beacon withdrawal snapshot: %w", err)
	}
	return result, nil
}
