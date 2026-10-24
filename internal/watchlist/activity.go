package watchlist

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

func decodeActivity(raw []byte, address string) (watchActivity, error) {
	var a watchActivity
	if json.Unmarshal(raw, &a) != nil {
		return a, ErrUnavailable
	}
	for _, n := range []string{a.BlockNumber, a.Timestamp, a.TransactionIndex} {
		if !validDecimal(n) {
			return a, ErrUnavailable
		}
	}
	if len(a.BlockHash) != 66 || len(a.TransactionHash) != 66 {
		return a, ErrUnavailable
	}
	for _, p := range []*string{a.From, a.To, a.TokenAddress} {
		if p != nil {
			if !common.IsHexAddress(*p) {
				return a, ErrUnavailable
			}
			*p = common.HexToAddress(*p).Hex()
		}
	}
	if a.From == nil {
		return a, ErrUnavailable
	}
	if a.Kind == "transaction" {
		if a.Value == nil {
			return a, ErrUnavailable
		}
		n, ok := new(big.Int).SetString(*a.Value, 0)
		if !ok || n.Sign() < 0 || n.BitLen() > 256 {
			return a, ErrUnavailable
		}
		v := n.String()
		a.Value = &v
		status := "unknown"
		if a.Status != nil {
			switch *a.Status {
			case "0x1":
				status = "success"
			case "0x0":
				status = "failed"
			default:
				return a, ErrUnavailable
			}
		}
		a.Status = &status
	} else {
		if a.Kind != "erc20" && a.Kind != "erc721" && a.Kind != "erc1155" {
			return a, ErrUnavailable
		}
		if a.TokenAddress == nil || a.LogIndex == nil || a.SubIndex == nil || a.EventKind == nil {
			return a, ErrUnavailable
		}
		for _, p := range []*string{a.Amount, a.TokenId, a.LogIndex, a.SubIndex} {
			if p != nil {
				if !validDecimal(*p) {
					return a, ErrUnavailable
				}
			}
		}
		if a.Decimals != nil {
			n, e := strconv.Atoi(*a.Decimals)
			if e != nil || n < 0 || n > 255 {
				return a, ErrUnavailable
			}
		}
	}
	incoming := a.To != nil && strings.EqualFold(*a.To, address)
	outgoing := strings.EqualFold(*a.From, address)
	switch {
	case incoming && outgoing:
		a.Direction = "self"
	case incoming:
		a.Direction = "in"
	case outgoing:
		a.Direction = "out"
	default:
		return a, ErrUnavailable
	}
	return a, nil
}
func validDecimal(s string) bool {
	n, ok := new(big.Int).SetString(s, 10)
	return ok && n.Sign() >= 0 && n.BitLen() <= 256 && n.String() == s
}

type watchActivity struct {
	Amount           *string `json:"amount,omitempty"`
	BlockHash        string  `json:"block_hash"`
	BlockNumber      string  `json:"block_number"`
	Decimals         *string `json:"decimals,omitempty"`
	Direction        string  `json:"direction"`
	EventKind        *string `json:"event_kind,omitempty"`
	From             *string `json:"from,omitempty"`
	Kind             string  `json:"kind"`
	LogIndex         *string `json:"log_index,omitempty"`
	Status           *string `json:"status,omitempty"`
	SubIndex         *string `json:"sub_index,omitempty"`
	Timestamp        string  `json:"timestamp"`
	To               *string `json:"to,omitempty"`
	TokenAddress     *string `json:"token_address,omitempty"`
	TokenId          *string `json:"token_id,omitempty"`
	TransactionHash  string  `json:"transaction_hash"`
	TransactionIndex string  `json:"transaction_index"`
	Value            *string `json:"value,omitempty"`
}
