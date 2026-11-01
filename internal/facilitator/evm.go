package facilitator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/x402-foundation/x402/go/v2/mechanisms/evm"
)

type authorizationContext struct{}
type chainClient struct {
	client  *ethclient.Client
	signer  digestSigner
	config  ChainConfig
	queries *dbgen.Queries
	guard   func(context.Context) error
}

var _ evm.FacilitatorEvmSigner = (*chainClient)(nil)

func (c *chainClient) GetAddresses() []string                           { return []string{c.signer.Address().Hex()} }
func (c *chainClient) GetChainID(ctx context.Context) (*big.Int, error) { return c.client.ChainID(ctx) }
func (c *chainClient) GetCode(ctx context.Context, address string) ([]byte, error) {
	return c.client.CodeAt(ctx, common.HexToAddress(address), nil)
}
func (c *chainClient) ReadContract(ctx context.Context, address string, raw []byte, method string, args ...any) (any, error) {
	a, err := abi.JSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	data, err := a.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	to := common.HexToAddress(address)
	out, err := c.client.CallContract(ctx, ethereum.CallMsg{From: c.signer.Address(), To: &to, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	values, err := a.Unpack(method, out)
	if err != nil {
		return nil, err
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return values, nil
}
func (c *chainClient) GetBalance(ctx context.Context, address, token string) (*big.Int, error) {
	raw := []byte(`[{"type":"function","name":"balanceOf","inputs":[{"name":"owner","type":"address"}],"outputs":[{"type":"uint256"}]}]`)
	value, err := c.ReadContract(ctx, token, raw, "balanceOf", common.HexToAddress(address))
	if err != nil {
		return nil, err
	}
	balance, ok := value.(*big.Int)
	if !ok {
		return nil, errors.New("invalid_balance")
	}
	return balance, nil
}
func (c *chainClient) VerifyTypedData(ctx context.Context, address string, domain evm.TypedDataDomain, fields map[string][]evm.TypedDataField, primary string, message map[string]any, signature []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	converted := apitypes.Types{}
	for name, items := range fields {
		for _, f := range items {
			converted[name] = append(converted[name], apitypes.Type{Name: f.Name, Type: f.Type})
		}
	}
	d := apitypes.TypedDataDomain{Name: domain.Name, Version: domain.Version, VerifyingContract: domain.VerifyingContract}
	if domain.ChainID != nil {
		d.ChainId = (*math.HexOrDecimal256)(domain.ChainID)
	}
	hash, _, err := apitypes.TypedDataAndHash(apitypes.TypedData{Types: converted, PrimaryType: primary, Domain: d, Message: message})
	if err != nil {
		return false, err
	}
	if len(signature) != 65 {
		return false, nil
	}
	sig := bytes.Clone(signature)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(hash, sig)
	return err == nil && crypto.PubkeyToAddress(*pub) == common.HexToAddress(address), nil
}
func (c *chainClient) SendTransaction(context.Context, string, []byte) (string, error) {
	return "", errors.New("arbitrary_transaction_disabled")
}
func (c *chainClient) WriteContract(ctx context.Context, address string, raw []byte, method string, suffix []byte, args ...any) (string, error) {
	if len(suffix) != 0 || (method != "transferWithAuthorization" && method != "settle") {
		return "", errors.New("unsupported_transaction")
	}
	allowed := method == "settle" && strings.EqualFold(address, evm.X402ExactPermit2ProxyAddress)
	for _, t := range c.config.Tokens {
		if method == "transferWithAuthorization" && strings.EqualFold(address, t.Address) {
			allowed = true
		}
	}
	if !allowed {
		return "", errors.New("unsupported_transaction")
	}
	a, err := abi.JSON(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	data, err := a.Pack(method, args...)
	if err != nil {
		return "", err
	}
	id, ok := ctx.Value(authorizationContext{}).(string)
	if !ok || id == "" {
		return "", errors.New("missing_reservation")
	}
	if err = c.guard(ctx); err != nil {
		return "", err
	}
	chainID, err := c.client.ChainID(ctx)
	expected, _ := networkID(c.config.Network)
	if err != nil || chainID == nil || chainID.Cmp(expected) != 0 {
		return "", errors.New("chain_identity_mismatch")
	}
	nonce, err := c.client.PendingNonceAt(ctx, c.signer.Address())
	if err != nil {
		return "", err
	}
	to := common.HexToAddress(address)
	tip, err := c.client.SuggestGasTipCap(ctx)
	if err != nil {
		return "", err
	}
	header, err := c.client.HeaderByNumber(ctx, nil)
	if err != nil || header.BaseFee == nil {
		return "", errors.New("fee_unavailable")
	}
	fee := new(big.Int).Add(tip, new(big.Int).Mul(header.BaseFee, big.NewInt(2)))
	cap, _ := new(big.Int).SetString(c.config.MaxFeePerGas, 10)
	if fee.Cmp(cap) > 0 {
		return "", errors.New("fee_limit")
	}
	gas, err := c.client.EstimateGas(ctx, ethereum.CallMsg{From: c.signer.Address(), To: &to, Data: data})
	if err != nil {
		return "", err
	}
	if gas > c.config.MaxGas || gas+gas/5 > c.config.MaxGas {
		return "", errors.New("gas_limit")
	}
	gas += gas / 5
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: fee, Gas: gas, To: &to, Data: data})
	txSigner := types.LatestSignerForChainID(chainID)
	sig, err := c.signer.Sign(ctx, txSigner.Hash(tx).Bytes())
	if err != nil {
		return "", err
	}
	signed, err := tx.WithSignature(txSigner, sig)
	if err != nil {
		return "", err
	}
	encoded, err := signed.MarshalBinary()
	if err != nil {
		return "", err
	}
	if err = c.guard(ctx); err != nil {
		return "", err
	}
	count, err := c.queries.FacilitatorSaveSigned(ctx, dbgen.FacilitatorSaveSignedParams{AuthorizationID: id, Nonce: pgtype.Numeric{Int: new(big.Int).SetUint64(nonce), Valid: true}, RawTransaction: encoded, TransactionHash: signed.Hash().Bytes()})
	if err != nil || count != 1 {
		return "", errors.New("journal_write_failed")
	}
	// Once durable, always return the original hash, even if broadcast is ambiguous.
	// The receipt wait and outer journal then report settlement_pending.
	if c.guard(ctx) == nil {
		if c.client.SendTransaction(ctx, signed) == nil {
			_, _ = c.queries.FacilitatorConfirmBroadcast(ctx, id, signed.Hash().Bytes())
		}
	}
	return signed.Hash().Hex(), nil
}
func (c *chainClient) WaitForTransactionReceipt(ctx context.Context, hash string) (*evm.TransactionReceipt, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		r, err := c.client.TransactionReceipt(ctx, common.HexToHash(hash))
		if err == nil && r.BlockNumber != nil {
			head, e := c.client.BlockNumber(ctx)
			if e == nil && head >= r.BlockNumber.Uint64() && head-r.BlockNumber.Uint64()+1 >= c.config.Confirmations {
				canonical, e := c.client.HeaderByNumber(ctx, r.BlockNumber)
				if e == nil && canonical.Hash() == r.BlockHash && r.TxHash == common.HexToHash(hash) {
					if id, ok := ctx.Value(authorizationContext{}).(string); ok && c.queries != nil {
						_, _ = c.queries.FacilitatorConfirmBroadcast(ctx, id, r.TxHash.Bytes())
					}
					return &evm.TransactionReceipt{Status: r.Status, BlockNumber: r.BlockNumber.Uint64(), TxHash: r.TxHash.Hex(), Logs: r.Logs}, nil
				}
			}
		} else if !receiptNotReady(err) {
			return nil, errors.New("receipt_unavailable")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Ensure SDK payload maps contain JSON-compatible scalar values in stored requests.
func canonicalJSON(v any) ([]byte, error) { return json.Marshal(v) }

// Geth can publish a block before its receipt index has caught up.
func receiptNotReady(err error) bool {
	if errors.Is(err, ethereum.NotFound) {
		return true
	}
	var rpcErr rpc.Error
	return errors.As(err, &rpcErr) && rpcErr.ErrorCode() == -32000 && rpcErr.Error() == "transaction indexing is in progress"
}
