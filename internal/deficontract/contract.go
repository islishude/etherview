// Package deficontract owns transport-independent DeFi publication contracts.
package deficontract

import (
	"context"
	"errors"
	"slices"
)

type Protocol string

const (
	UniswapV2 Protocol = "uniswap-v2"
	UniswapV3 Protocol = "uniswap-v3"
	UniswapV4 Protocol = "uniswap-v4"
	AaveV3    Protocol = "aave-v3"
	Curve     Protocol = "curve"
	Lido      Protocol = "lido"
)

func Protocols() []Protocol { return []Protocol{UniswapV2, UniswapV3, UniswapV4, AaveV3, Curve, Lido} }
func (p Protocol) Valid() bool {
	return slices.Contains(Protocols(), p)
}

var (
	ErrInput       = errors.New("defi_invalid_input")
	ErrCursor      = errors.New("defi_invalid_cursor")
	ErrUnavailable = errors.New("defi_unavailable")
	ErrIntegrity   = errors.New("defi_integrity")
	ErrUnsupported = errors.New("defi_unsupported")
)

type Asset struct {
	Kind     string `json:"kind"`
	Address  string `json:"address,omitempty"`
	Amount   string `json:"amount"`
	Decimals *int   `json:"decimals,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
}
type Range struct {
	LowerTick    int32  `json:"lower_tick"`
	UpperTick    int32  `json:"upper_tick"`
	CurrentTick  int32  `json:"current_tick"`
	SqrtPriceX96 string `json:"sqrt_price_x96"`
	InRange      bool   `json:"in_range"`
}
type Position struct {
	ID            string   `json:"id"`
	Protocol      Protocol `json:"protocol"`
	Deployment    string   `json:"deployment"`
	Owner         string   `json:"owner"`
	Kind          string   `json:"kind"`
	Contract      string   `json:"contract"`
	TokenID       string   `json:"token_id,omitempty"`
	Pool          string   `json:"pool,omitempty"`
	PoolID        string   `json:"pool_id,omitempty"`
	Hook          string   `json:"hook,omitempty"`
	Fee           *uint32  `json:"fee,omitempty"`
	TickSpacing   *int32   `json:"tick_spacing,omitempty"`
	Liquidity     string   `json:"liquidity,omitempty"`
	ReceiptAsset  *Asset   `json:"receipt_asset,omitempty"`
	ReceiptAmount string   `json:"receipt_amount,omitempty"`
	Range         *Range   `json:"range,omitempty"`
	Collateral    *bool    `json:"collateral,omitempty"`
	Assets        []Asset  `json:"assets"`
	Debts         []Asset  `json:"debts"`
	Claimable     []Asset  `json:"claimable"`
	UnsettledFees []Asset  `json:"unsettled_fees"`
	State         string   `json:"state"`
	Limitations   []string `json:"limitations"`
	BlockNumber   string   `json:"block_number"`
	BlockHash     string   `json:"block_hash"`
}
type DeploymentStatus struct {
	Protocol      Protocol `json:"protocol"`
	Deployment    string   `json:"deployment"`
	State         string   `json:"state"`
	BlockNumber   string   `json:"block_number,omitempty"`
	BlockHash     string   `json:"block_hash,omitempty"`
	CoverageStart string   `json:"coverage_start,omitempty"`
	CoverageEnd   string   `json:"coverage_end,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
	FailureCode   string   `json:"failure_code,omitempty"`
}
type Page struct {
	Positions   []Position         `json:"positions"`
	Deployments []DeploymentStatus `json:"deployments"`
	NextCursor  string             `json:"-"`
}
type Request struct {
	Owner, Cursor string
	Protocol      Protocol
	Limit         int
}
type Reader interface {
	Positions(context.Context, Request) (Page, error)
}
