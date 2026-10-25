package verify

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrTargetUnavailable = errors.New("canonical verification target is unavailable")

// ResolveTarget copies authoritative local facts without retaining a snapshot.
// Neither adapters nor source providers may select the bytecode or block hash.
func ResolveTarget(ctx context.Context, db dbgen.DBTX, chain uint64, address string, maximum int) (VerificationTarget, error) {
	if db == nil || chain == 0 || !common.IsHexAddress(address) {
		return VerificationTarget{}, ErrTargetUnavailable
	}
	if maximum <= 0 {
		maximum = defaultCompilerInputBytes
	}
	address = strings.ToLower(common.HexToAddress(address).Hex())
	var chainID pgtype.Numeric
	if err := chainID.Scan(strconv.FormatUint(chain, 10)); err != nil {
		return VerificationTarget{}, err
	}
	row, err := dbgen.New(db).VerifyResolveTarget(ctx, common.HexToAddress(address).Bytes(), chainID, address)
	if errors.Is(err, pgx.ErrNoRows) {
		return VerificationTarget{}, ErrTargetUnavailable
	}
	if err != nil {
		return VerificationTarget{}, err
	}
	if len(row.CodeHash) != 32 || len(row.BlockHash) != 32 || len(row.Code) == 0 || len(row.Code) > maximum || !bytes.Equal(crypto.Keccak256(row.Code), row.CodeHash) {
		return VerificationTarget{}, ErrTargetUnavailable
	}
	creation := ""
	if row.CreationBytecodePresent {
		code, decodeErr := decodeBytecode(row.CreationBytecode)
		if decodeErr != nil || len(code) == 0 || len(code) > maximum {
			return VerificationTarget{}, ErrTargetUnavailable
		}
		creation = "0x" + hex.EncodeToString(code)
	} else if !row.GenesisPredeploy {
		return VerificationTarget{}, ErrTargetUnavailable
	}
	return VerificationTarget{ChainID: chain, Address: address, CodeHash: "0x" + hex.EncodeToString(row.CodeHash), AtBlockHash: "0x" + hex.EncodeToString(row.BlockHash), CreationBytecode: creation, RuntimeBytecode: "0x" + hex.EncodeToString(row.Code), GenesisPredeploy: row.GenesisPredeploy && creation == ""}, nil
}
