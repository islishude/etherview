package etherscan

import (
	"context"
	"fmt"
	"net/url"

	"github.com/islishude/etherview/internal/db/gen"
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
	defer tx.Rollback() //nolint:errcheck
	if _, err := b.requireCanonicalCoreRange(ctx, tx, start, end); err != nil {
		return nil, err
	}
	var endArgument any
	if end != nil {
		endArgument = *end
	}
	rows, err := tx.QueryContext(ctx, dbgen.EtherscanBeaconWithdrawals,
		b.chain, address, start, endArgument, page.limit, page.offset, page.direction,
	)
	if err != nil {
		return nil, fmt.Errorf("query beacon withdrawals: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	result := make([]beaconWithdrawal, 0, page.limit)
	for rows.Next() {
		var item beaconWithdrawal
		var addressBytes []byte
		if err := rows.Scan(
			&item.WithdrawalIndex, &item.ValidatorIndex, &addressBytes,
			&item.Amount, &item.BlockNumber, &item.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scan beacon withdrawal: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate beacon withdrawals: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close beacon withdrawals: %w", err)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit beacon withdrawal snapshot: %w", err)
	}
	return result, nil
}
