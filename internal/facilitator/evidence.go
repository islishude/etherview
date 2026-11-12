package facilitator

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/x402settlement"
)

var transferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
var authorizationUsedTopic = crypto.Keccak256Hash([]byte("AuthorizationUsed(address,bytes32)"))
var settledTopic = crypto.Keccak256Hash([]byte("Settled()"))

type scanCursor struct {
	BackNext   bool        `json:"backNext"`
	Back       int64       `json:"back"`
	Forward    uint64      `json:"forward"`
	Anchor     uint64      `json:"anchor"`
	AnchorHash common.Hash `json:"anchorHash"`
}

func transferLog(receipt *types.Receipt, in paymentRequest, payer common.Address) *types.Log {
	amount, ok := new(big.Int).SetString(in.PaymentRequirements.Amount, 10)
	if !ok {
		return nil
	}
	for _, log := range receipt.Logs {
		if !log.Removed && log.TxHash == receipt.TxHash && log.BlockHash == receipt.BlockHash && log.BlockNumber == receipt.BlockNumber.Uint64() && log.Address == common.HexToAddress(in.PaymentRequirements.Asset) && len(log.Topics) == 3 && log.Topics[0] == transferTopic && log.Topics[1] == common.BytesToHash(payer.Bytes()) && log.Topics[2] == common.BytesToHash(common.HexToAddress(in.PaymentRequirements.PayTo).Bytes()) && len(log.Data) == 32 && new(big.Int).SetBytes(log.Data).Cmp(amount) == 0 {
			return log
		}
	}
	return nil
}

func confirmedReceipt(ctx context.Context, ch *chainRuntime, hash common.Hash) (*types.Receipt, *types.Header, error) {
	receipt, err := ch.adapter.client.TransactionReceipt(ctx, hash)
	if err != nil || receipt == nil || receipt.BlockNumber == nil || receipt.TxHash != hash {
		return nil, nil, errors.New("receipt_unavailable")
	}
	head, err := ch.adapter.client.BlockNumber(ctx)
	if err != nil || head < receipt.BlockNumber.Uint64() || head-receipt.BlockNumber.Uint64()+1 < ch.adapter.config.Confirmations {
		return nil, nil, errors.New("receipt_unconfirmed")
	}
	header, err := ch.adapter.client.HeaderByNumber(ctx, receipt.BlockNumber)
	if err != nil || header.Hash() != receipt.BlockHash {
		return nil, nil, errors.New("receipt_noncanonical")
	}
	return receipt, header, nil
}

func evidenceFrom(receipt *types.Receipt, log *types.Log, in paymentRequest, payer common.Address, source string) *x402settlement.Evidence {
	return &x402settlement.Evidence{Transaction: receipt.TxHash.Hex(), BlockHash: receipt.BlockHash.Hex(), BlockNumber: receipt.BlockNumber.String(), LogIndex: strconv.FormatUint(uint64(log.Index), 10), Source: source, Network: in.PaymentRequirements.Network, Payer: payer.Hex(), Recipient: common.HexToAddress(in.PaymentRequirements.PayTo).Hex(), Asset: common.HexToAddress(in.PaymentRequirements.Asset).Hex(), Amount: in.PaymentRequirements.Amount}
}

func externalEvidence(ctx context.Context, ch *chainRuntime, in paymentRequest, proof *authorizationProof, hash common.Hash) (*x402settlement.Evidence, error) {
	tx, pending, err := ch.adapter.client.TransactionByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	chain, _ := networkID(in.PaymentRequirements.Network)
	if pending || tx.Hash() != hash || !proof.matchesTransaction(tx, chain) {
		return nil, nil
	}
	receipt, header, err := confirmedReceipt(ctx, ch, hash)
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, nil
	}
	timestamp := new(big.Int).SetUint64(header.Time)
	if timestamp.Cmp(proof.after) <= 0 && proof.method == "eip3009" || timestamp.Cmp(proof.after) < 0 || timestamp.Cmp(proof.before) >= 0 && proof.method == "eip3009" || timestamp.Cmp(proof.before) > 0 {
		return nil, nil
	}
	log := transferLog(receipt, in, proof.payer)
	if log == nil {
		return nil, nil
	}
	matched := false
	for _, event := range receipt.Logs {
		if event.Removed || event.TxHash != hash || event.BlockHash != receipt.BlockHash {
			continue
		}
		if proof.method == "eip3009" && event.Address == proof.token && len(event.Topics) == 3 && event.Topics[0] == authorizationUsedTopic && event.Topics[1] == common.BytesToHash(proof.payer.Bytes()) && event.Topics[2] == proof.nonceBytes && len(event.Data) == 0 {
			matched = true
		}
		if proof.method == "permit2" && event.Address == proof.target && len(event.Topics) == 1 && event.Topics[0] == settledTopic && len(event.Data) == 0 {
			matched = true
		}
	}
	if !matched {
		return nil, nil
	}
	return evidenceFrom(receipt, log, in, proof.payer, "external"), nil
}

// One bounded page per cycle; failed or oversized pages never advance coverage.
// Dense single blocks stay pending rather than silently losing candidates.
func (s *Service) scanExternal(ctx context.Context, ch *chainRuntime, row dbgen.FacilitatorSettlement, in paymentRequest, proof *authorizationProof) (*x402settlement.Evidence, error) {
	head, err := ch.adapter.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, err
	}
	if head.Number.Uint64()+1 < ch.adapter.config.Confirmations {
		return nil, nil
	}
	end := head.Number.Uint64() + 1 - ch.adapter.config.Confirmations
	anchor, err := ch.adapter.client.HeaderByNumber(ctx, new(big.Int).SetUint64(end))
	if err != nil {
		return nil, err
	}
	cursor := scanCursor{Back: int64(end), Forward: end + 1, Anchor: end, AnchorHash: anchor.Hash()}
	if len(row.ScanCursor) > 0 {
		if json.Unmarshal(row.ScanCursor, &cursor) != nil {
			return nil, errors.New("invalid_scan_cursor")
		}
		prior, e := ch.adapter.client.HeaderByNumber(ctx, new(big.Int).SetUint64(cursor.Anchor))
		if e != nil {
			return nil, e
		}
		if prior.Hash() != cursor.AnchorHash {
			cursor = scanCursor{Back: int64(end), Forward: end + 1, Anchor: end, AnchorHash: anchor.Hash()}
		}
	}
	// Alternate attempted pages when both directions have work. Otherwise a
	// continuously advancing head can indefinitely starve historical payments.
	forward := cursor.Forward <= end && (cursor.Back < 0 || !cursor.BackNext)
	var from, to uint64
	if forward {
		from = cursor.Forward
		to = min(end, from+2047)
	} else {
		if cursor.Back < 0 {
			return nil, nil
		}
		to = uint64(cursor.Back)
		if to > 2047 {
			from = to - 2047
		}
	}
	// Checkpoint the observation boundary and next direction before RPC work.
	// Coverage does not advance here. A failed/dense historical page must not
	// starve forward work, and interrupted rounds must retain their first anchor.
	cursor.BackNext = forward
	saveCursor := func() error {
		encoded, _ := json.Marshal(cursor)
		count, err := s.queries.FacilitatorScanCursor(ctx, row.AuthorizationID, encoded)
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("journal_changed")
		}
		return nil
	}
	if err := saveCursor(); err != nil {
		return nil, err
	}
	query := ethereum.FilterQuery{Addresses: []common.Address{proof.token}, Topics: [][]common.Hash{{transferTopic}, {common.BytesToHash(proof.payer.Bytes())}, {common.BytesToHash(proof.recipient.Bytes())}}}
	var logs []types.Log
	for {
		query.FromBlock = new(big.Int).SetUint64(from)
		query.ToBlock = new(big.Int).SetUint64(to)
		logs, err = ch.adapter.client.FilterLogs(ctx, query)
		if err == nil && len(logs) <= 32 {
			break
		}
		if from == to {
			return nil, errors.New("evidence_scan_unavailable")
		}
		if forward {
			to = from + (to-from)/2
		} else {
			from = to - (to-from)/2
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	seen := map[common.Hash]bool{}
	for _, log := range logs {
		if log.Removed || log.BlockNumber < from || log.BlockNumber > to {
			return nil, errors.New("invalid_scan_log")
		}
		if seen[log.TxHash] {
			continue
		}
		seen[log.TxHash] = true
		evidence, e := externalEvidence(ctx, ch, in, proof, log.TxHash)
		if e != nil {
			return nil, e
		}
		if evidence != nil {
			return evidence, nil
		}
	}
	check, err := ch.adapter.client.HeaderByNumber(ctx, new(big.Int).SetUint64(end))
	if err != nil || check.Hash() != anchor.Hash() {
		return nil, errors.New("scan_reorganized")
	}
	if forward {
		cursor.Forward = to + 1
		cursor.Anchor = end
		cursor.AnchorHash = anchor.Hash()
	} else {
		cursor.Back = int64(from) - 1
	}
	return nil, saveCursor()
}
