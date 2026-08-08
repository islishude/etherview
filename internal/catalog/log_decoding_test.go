package catalog

import (
	"database/sql"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/enrich"
)

func TestPublicPersistedLogDecodingPreservesIndexedHashAndExactSource(t *testing.T) {
	t.Parallel()
	address := common.HexToAddress("0x1111111111111111111111111111111111111111")
	codeHash := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	decoded, found, err := publicPersistedLogDecoding(persistedLogDecoding{
		status:        sql.NullString{String: "decoded", Valid: true},
		signature:     sql.NullString{String: "Changed(string)", Valid: true},
		source:        sql.NullString{String: "verified", Valid: true},
		confidence:    sql.NullString{String: "verified", Valid: true},
		arguments:     []byte(`[{"name":"value","type":"string","indexed":true,"hashed":true,"value":"0x01"}]`),
		candidates:    []byte(`["Changed(string)"]`),
		targetAddress: address[:], targetCodeHash: codeHash[:],
	})
	if err != nil || !found || decoded.EventName != "Changed" || len(decoded.Arguments) != 1 ||
		!decoded.Arguments[0].Hashed || decoded.ABISource == nil ||
		decoded.ABISource.Kind != "exact_address" || decoded.ABISource.Address != address.Hex() ||
		decoded.ABISource.CodeHash != codeHash.Hex() {
		t.Fatalf("found=%t decoded=%+v error=%v", found, decoded, err)
	}
}

func TestPublicDecodeResultDoesNotSelectAnAmbiguousCandidate(t *testing.T) {
	t.Parallel()
	decoded := publicDecodeResult(enrich.DecodeResult{
		Status: enrich.DecodeAmbiguous, Kind: enrich.ABIKindEvent,
		Name: "First", Signature: "First(uint256)", Source: enrich.ABISourceCodeHash,
		Confidence: enrich.ConfidenceHigh,
		Arguments:  []enrich.DecodedArgument{{Name: "value", Type: "uint256", Value: "1"}},
		Candidates: []string{"First(uint256)", "Second(uint256)"},
	}, map[enrich.ABISource]TransactionLogABISource{
		enrich.ABISourceCodeHash: {Kind: "code_hash"},
	})
	if decoded.Status != "ambiguous" || decoded.Signature != "" || decoded.EventName != "" ||
		len(decoded.Arguments) != 0 || decoded.ABISource != nil || len(decoded.Candidates) != 2 {
		t.Fatalf("ambiguous decoding=%+v", decoded)
	}
}
