package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// ImportV2 retrieves compiler input only. Publication uses the ordinary address
// job, its pinned compiler, local matching and canonical/lease fences.
func (c *SourcifyClient) ImportV2(ctx context.Context, sourceChainID uint64, target VerificationTarget) (SubmissionV2, error) {
	request := SubmissionV2{Kind: JobAddress, Target: &target, Bytecodes: []BytecodePair{{Creation: target.CreationBytecode, Runtime: target.RuntimeBytecode}}}
	if c == nil || sourceChainID == 0 || len(target.RuntimeBytecode)+len(target.CreationBytecode) > c.maxRequestBytes || !validAddressVerificationSubmission(&request) {
		return SubmissionV2{}, ErrVerificationTargetInvalid
	}
	contract, err := c.Lookup(ctx, sourceChainID, target.Address)
	if err != nil {
		return SubmissionV2{}, err
	}
	remote, remoteErr := decodeBytecode(contract.RuntimeBytecode.OnchainBytecode)
	local, _ := decodeBytecode(target.RuntimeBytecode)
	if contract.Match == "" || contract.RuntimeMatch == "" || remoteErr != nil || !bytes.Equal(remote, local) {
		return SubmissionV2{}, ErrSourcifyTargetMismatch
	}
	compilation := contract.Compilation
	if !jsonObject(contract.StandardJSON) || len(contract.StandardJSON) > c.maxRequestBytes || !validContractIdentifier(compilation.FullyQualifiedName) {
		return SubmissionV2{}, ErrSourcifyInvalidResponse
	}
	request.CompilerVersion = compilation.CompilerVersion
	switch {
	case compilation.Language == "Solidity" && compilation.Compiler == "solc":
		request.Language = LanguageSolidity
		// Both inputs use solc; the Standard JSON language selects the local
		// execution path even when upstream compilation is classified as Solidity.
		var input struct {
			Language string `json:"language"`
		}
		if json.Unmarshal(contract.StandardJSON, &input) == nil && input.Language == "Yul" {
			request.Language = LanguageYul
		}
	case compilation.Language == "Yul" && compilation.Compiler == "solc":
		request.Language = LanguageYul
	case compilation.Language == "Vyper" && compilation.Compiler == "vyper":
		version, ok := importedVyperReleaseVersion(compilation.CompilerVersion)
		if !ok {
			return SubmissionV2{}, ErrSourcifyInvalidResponse
		}
		request.CompilerVersion = version
		request.Language = LanguageVyper
		request.TargetFile = compilation.FullyQualifiedName[:strings.LastIndex(compilation.FullyQualifiedName, ":")]
	default:
		return SubmissionV2{}, ErrSourcifyInvalidResponse
	}
	request.ContractNameHint = compilation.FullyQualifiedName
	request.StandardJSON = append(json.RawMessage(nil), contract.StandardJSON...)
	// Apply the same bounded inline-source preparation before accepting remote
	// data. The service prepares again at its normal public submission boundary.
	service := Service{maxInputBytes: c.maxRequestBytes}
	prepared := request
	if service.prepareV2(ctx, &prepared) != nil {
		return SubmissionV2{}, ErrSourcifyInvalidResponse
	}
	return request, nil
}
