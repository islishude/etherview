package etherscan

import (
	"context"
	"net/url"
	"strings"
)

type accountBalance struct {
	Account string `json:"account"`
	Balance string `json:"balance"`
}

func (b *PostgresBackend) nativeBalance(ctx context.Context, values url.Values) (string, error) {
	address, _, err := parseAddressParameter(values.Get("address"), "address")
	if err != nil {
		return "", err
	}
	if err := latestTag(values); err != nil {
		return "", err
	}
	balances, err := b.fixedNativeBalances(ctx, []string{address.String()})
	if err != nil {
		return "", err
	}
	return balances[0], nil
}

func (b *PostgresBackend) nativeBalances(ctx context.Context, values url.Values) ([]accountBalance, error) {
	if err := latestTag(values); err != nil {
		return nil, err
	}
	raw := strings.Split(values.Get("address"), ",")
	if len(raw) == 0 || len(raw) > 20 {
		return nil, invalidParameter("address must contain between 1 and 20 accounts")
	}
	addresses := make([]string, len(raw))
	checksummed := make([]string, len(raw))
	for index, item := range raw {
		address, _, err := parseAddressParameter(strings.TrimSpace(item), "address")
		if err != nil {
			return nil, err
		}
		addresses[index] = address.String()
		checksummed[index], err = checksumAddress(address)
		if err != nil {
			return nil, err
		}
	}
	balances, err := b.fixedNativeBalances(ctx, addresses)
	if err != nil {
		return nil, err
	}
	result := make([]accountBalance, len(balances))
	for index := range balances {
		result[index] = accountBalance{Account: checksummed[index], Balance: balances[index]}
	}
	return result, nil
}

func (b *PostgresBackend) fixedNativeBalances(ctx context.Context, addresses []string) ([]string, error) {
	if b.state == nil {
		return nil, ErrStateUnavailable
	}
	balances, err := b.state.NativeBalances(ctx, addresses)
	if err != nil || len(balances) != len(addresses) {
		return nil, ErrStateUnavailable
	}
	for _, balance := range balances {
		if _, err := parseCanonicalDecimal(balance); err != nil {
			return nil, ErrStateUnavailable
		}
	}
	return balances, nil
}

func latestTag(values url.Values) error {
	if tag := strings.TrimSpace(values.Get("tag")); tag != "" && tag != "latest" {
		return invalidParameter("tag must be latest")
	}
	return nil
}
