package etherscan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidParameter is returned when Execute is used without the Handler's
// validation layer. Keeping validation in the backend prevents malformed
// values from ever reaching dynamically assembled (but still parameterized)
// queries.
var ErrInvalidParameter = errors.New("invalid parameter")

// SupplyProvider returns the execution-layer native currency supply in wei.
// Ethereum JSON-RPC has no standard total-supply method and the core index does
// not persist genesis allocation or issuance, so callers must wire an explicit
// authoritative provider rather than receiving a fabricated value.
type SupplyProvider func(context.Context, uint64) (string, error)

type NativePrice struct {
	USD        string
	BTC        string
	ObservedAt time.Time
}

type PriceProvider func(context.Context) (NativePrice, error)

// StateProvider returns current values from one fixed canonical block and
// rechecks that block after the RPC response. Event-derived ledgers do not
// satisfy this interface without an explicit reconciliation at that block.
type StateProvider interface {
	NativeBalances(context.Context, []string) ([]string, error)
	ERC20Balance(context.Context, string, string) (string, error)
	ERC20TotalSupply(context.Context, string) (string, error)
}

type PostgresOptions struct {
	ChainID                   uint64
	Supply                    SupplyProvider
	Price                     PriceProvider
	State                     StateProvider
	Verification              VerificationService
	VerificationMaxInputBytes int
}

// PostgresBackend serves the Etherscan-compatible subset that can be proven
// from canonical core rows and verified-contract records.
type PostgresBackend struct {
	db                        *sql.DB
	chainID                   uint64
	chain                     string
	supply                    SupplyProvider
	price                     PriceProvider
	state                     StateProvider
	verification              VerificationService
	maxVerificationInputBytes int
}

var _ Backend = (*PostgresBackend)(nil)

func NewPostgresBackend(db *sql.DB, options PostgresOptions) (*PostgresBackend, error) {
	if db == nil {
		return nil, errors.New("etherscan database is nil")
	}
	if options.ChainID == 0 {
		return nil, errors.New("etherscan chain ID must be greater than zero")
	}
	maximum := options.VerificationMaxInputBytes
	if maximum <= 0 {
		maximum = defaultVerificationInputBytes
	}
	return &PostgresBackend{
		db: db, chainID: options.ChainID,
		chain: strconv.FormatUint(options.ChainID, 10), supply: options.Supply, price: options.Price, state: options.State,
		verification: options.Verification, maxVerificationInputBytes: maximum,
	}, nil
}

func (b *PostgresBackend) Execute(ctx context.Context, request Request) (any, error) {
	if b == nil || b.db == nil || b.chainID == 0 {
		return nil, errors.New("etherscan backend is not initialized")
	}
	if request.Values == nil {
		request.Values = make(url.Values)
	}
	switch request.Module + "." + request.Action {
	case "account.balance":
		return b.nativeBalance(ctx, request.Values)
	case "account.balancemulti":
		return b.nativeBalances(ctx, request.Values)
	case "account.txlist":
		return b.accountTransactions(ctx, request.Values)
	case "account.txlistinternal":
		return b.internalTransactions(ctx, request.Values)
	case "account.tokentx", "account.tokennfttx", "account.token1155tx":
		return b.accountTokenTransfers(ctx, request.Action, request.Values)
	case "account.tokenbalance":
		return b.tokenBalance(ctx, request.Values)
	case "account.getminedblocks":
		return b.minedBlocks(ctx, request.Values)

	case "transaction.getstatus":
		return b.transactionStatus(ctx, request.Values, false)
	case "transaction.gettxreceiptstatus":
		return b.transactionStatus(ctx, request.Values, true)

	case "logs.getLogs":
		return b.logs(ctx, request.Values)

	case "block.getblocknobytime":
		return b.blockNumberByTime(ctx, request.Values)
	case "block.getblockcountdown":
		return b.blockCountdown(ctx, request.Values)

	case "stats.ethsupply":
		return b.ethSupply(ctx)
	case "stats.ethprice":
		return b.ethPrice(ctx)
	case "stats.tokensupply":
		return b.tokenSupply(ctx, request.Values)

	case "contract.getabi":
		return b.contractABI(ctx, request.Values)
	case "contract.getsourcecode":
		return b.contractSource(ctx, request.Values)
	case "contract.getcontractcreation":
		return b.contractCreation(ctx, request.Values)
	case "contract.verifysourcecode":
		return b.submitSourceVerification(ctx, request.Values)
	case "contract.checkverifystatus":
		return b.sourceVerificationStatus(ctx, request.Values)
	case "contract.verifyproxycontract", "contract.checkproxyverification":
		return nil, ErrProxyVerificationUnavailable

	case "token.tokensupply":
		return b.tokenSupply(ctx, request.Values)
	case "token.tokenbalance":
		return b.tokenBalance(ctx, request.Values)
	case "token.tokeninfo":
		return b.tokenInformation(ctx, request.Values)
	case "token.tokenholderlist":
		return b.tokenHolders(ctx, request.Values)
	default:
		return nil, invalidParameter("unsupported module/action %q/%q", request.Module, request.Action)
	}
}

func (b *PostgresBackend) ethPrice(ctx context.Context) (any, error) {
	if b.price == nil {
		return nil, ErrPriceUnavailable
	}
	price, err := b.price(ctx)
	if err != nil {
		return nil, ErrPriceUnavailable
	}
	if price.USD == "" || price.BTC == "" || price.ObservedAt.IsZero() {
		return nil, ErrPriceUnavailable
	}
	timestamp := strconv.FormatInt(price.ObservedAt.UTC().Unix(), 10)
	return struct {
		ETHBTC          string `json:"ethbtc"`
		ETHBTCTimestamp string `json:"ethbtc_timestamp"`
		ETHUSD          string `json:"ethusd"`
		ETHUSDTimestamp string `json:"ethusd_timestamp"`
	}{
		ETHBTC: price.BTC, ETHBTCTimestamp: timestamp,
		ETHUSD: price.USD, ETHUSDTimestamp: timestamp,
	}, nil
}

type pagination struct {
	limit     int
	offset    int64
	direction string
}

func parsePagination(values url.Values) (pagination, error) {
	page, err := parsePositiveInt(values.Get("page"), 1, math.MaxInt64)
	if err != nil {
		return pagination{}, invalidParameter("page: %v", err)
	}
	limit, err := parsePositiveInt(values.Get("offset"), 100, 1000)
	if err != nil {
		return pagination{}, invalidParameter("offset: %v", err)
	}
	if page > math.MaxInt64/int64(limit) {
		return pagination{}, invalidParameter("page and offset are too large")
	}
	direction := strings.ToUpper(strings.TrimSpace(values.Get("sort")))
	if direction == "" {
		direction = "ASC"
	}
	if direction != "ASC" && direction != "DESC" {
		return pagination{}, invalidParameter("sort must be asc or desc")
	}
	return pagination{limit: int(limit), offset: (page - 1) * int64(limit), direction: direction}, nil
}

func parsePositiveInt(raw string, fallback, maximum int64) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("must be between 1 and %d", maximum)
	}
	return value, nil
}

func parseDecimal(raw, name string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, invalidParameter("%s is required", name)
	}
	value, err := parseCanonicalDecimal(raw)
	if err != nil {
		return nil, invalidParameter("%s must be a canonical non-negative decimal integer", name)
	}
	return value, nil
}

func parseCanonicalDecimal(raw string) (*big.Int, error) {
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.Sign() < 0 || value.BitLen() > 256 || len(raw) > 1 && raw[0] == '0' {
		return nil, errors.New("not a canonical uint256 decimal")
	}
	return value, nil
}

func decimalRange(values url.Values) (string, *string, error) {
	start := "0"
	if raw := strings.TrimSpace(values.Get("startblock")); raw != "" {
		value, err := parseDecimal(raw, "startblock")
		if err != nil {
			return "", nil, err
		}
		start = value.String()
	}
	var end *string
	if raw := strings.TrimSpace(values.Get("endblock")); raw != "" {
		value, err := parseDecimal(raw, "endblock")
		if err != nil {
			return "", nil, err
		}
		text := value.String()
		end = &text
		if value.Cmp(mustBig(start)) < 0 {
			return "", nil, invalidParameter("endblock is less than startblock")
		}
	}
	return start, end, nil
}

func mustBig(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("non-decimal internal value")
	}
	return result
}

func invalidParameter(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidParameter, fmt.Sprintf(format, arguments...))
}
