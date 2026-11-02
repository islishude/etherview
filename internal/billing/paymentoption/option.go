// Package paymentoption defines the public, secret-free payment policy snapshot.
package paymentoption

import (
	"errors"
	"math/big"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const CreditDecimals = 6

var ErrInvalid = errors.New("invalid payment option or amount")
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var networkPattern = regexp.MustCompile(`^eip155:[1-9][0-9]{0,88}$`)

type Option struct {
	ID                       string   `yaml:"id" json:"id"`
	Network                  string   `yaml:"-" json:"network"`
	Name                     string   `yaml:"name" json:"name"`
	Asset                    string   `yaml:"asset" json:"asset"`
	AssetDecimals            *uint8   `yaml:"asset_decimals" json:"asset_decimals"`
	AssetEIP712Name          string   `yaml:"asset_eip712_name" json:"asset_eip712_name,omitempty"`
	AssetEIP712Version       string   `yaml:"asset_eip712_version" json:"asset_eip712_version,omitempty"`
	Recipient                string   `yaml:"recipient" json:"recipient"`
	AssetTransferMethods     []string `yaml:"asset_transfer_methods" json:"asset_transfer_methods"`
	MinimumTopupAmountAtomic string   `yaml:"minimum_topup_amount_atomic" json:"minimum_topup_amount_atomic"`
	MaximumTopupAmountAtomic string   `yaml:"maximum_topup_amount_atomic" json:"maximum_topup_amount_atomic"`
	CreditRateNumerator      string   `yaml:"credit_rate_numerator" json:"credit_rate_numerator"`
	CreditRateDenominator    string   `yaml:"credit_rate_denominator" json:"credit_rate_denominator"`
}

func positive(value string) (*big.Int, bool) {
	if len(value) == 0 || len(value) > 78 {
		return nil, false
	}
	n, ok := new(big.Int).SetString(value, 10)
	return n, ok && n.Sign() > 0 && n.BitLen() <= 256 && n.String() == value
}
func (o Option) Validate() error {
	if !idPattern.MatchString(o.ID) || !networkPattern.MatchString(o.Network) || o.AssetDecimals == nil || len(o.Name) == 0 || len(o.Name) > 128 || strings.TrimSpace(o.Name) != o.Name {
		return ErrInvalid
	}
	for _, a := range []string{o.Asset, o.Recipient} {
		if len(a) != 42 || !strings.HasPrefix(a, "0x") || !common.IsHexAddress(a) || common.HexToAddress(a) == (common.Address{}) {
			return ErrInvalid
		}
	}
	min, ok := positive(o.MinimumTopupAmountAtomic)
	if !ok {
		return ErrInvalid
	}
	max, ok := positive(o.MaximumTopupAmountAtomic)
	if !ok || min.Cmp(max) > 0 {
		return ErrInvalid
	}
	if _, ok = positive(o.CreditRateNumerator); !ok {
		return ErrInvalid
	}
	if _, ok = positive(o.CreditRateDenominator); !ok {
		return ErrInvalid
	}
	if len(o.AssetEIP712Name) > 128 || len(o.AssetEIP712Version) > 32 || strings.TrimSpace(o.AssetEIP712Name) != o.AssetEIP712Name || strings.TrimSpace(o.AssetEIP712Version) != o.AssetEIP712Version {
		return ErrInvalid
	}
	if len(o.AssetTransferMethods) < 1 || len(o.AssetTransferMethods) > 2 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, m := range o.AssetTransferMethods {
		if (m != "eip3009" && m != "permit2") || seen[m] {
			return ErrInvalid
		}
		seen[m] = true
		if m == "eip3009" && (o.AssetEIP712Name == "" || len(o.AssetEIP712Name) > 128 || o.AssetEIP712Version == "" || len(o.AssetEIP712Version) > 32 || strings.TrimSpace(o.AssetEIP712Name) != o.AssetEIP712Name || strings.TrimSpace(o.AssetEIP712Version) != o.AssetEIP712Version) {
			return ErrInvalid
		}
	}
	return nil
}
func (o Option) Credit(amount string) (string, error) {
	if o.Validate() != nil {
		return "", ErrInvalid
	}
	value, ok := positive(amount)
	if !ok {
		return "", ErrInvalid
	}
	min, _ := positive(o.MinimumTopupAmountAtomic)
	max, _ := positive(o.MaximumTopupAmountAtomic)
	if value.Cmp(min) < 0 || value.Cmp(max) > 0 {
		return "", ErrInvalid
	}
	numerator, _ := positive(o.CreditRateNumerator)
	denominator, _ := positive(o.CreditRateDenominator)
	quotient, remainder := new(big.Int).QuoRem(new(big.Int).Mul(value, numerator), denominator, new(big.Int))
	if remainder.Sign() != 0 || quotient.Sign() <= 0 || quotient.BitLen() > 256 {
		return "", ErrInvalid
	}
	return quotient.String(), nil
}
