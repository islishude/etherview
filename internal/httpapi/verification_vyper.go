package httpapi

import (
	"errors"
	"github.com/islishude/etherview/internal/verify"
)

func validateVyperTransport(input verifierSubmission, vyper bool) error {
	if !vyper {
		if input.TargetFile != "" || input.OptimizationMode != "" || len(input.Interfaces) > 0 || input.Language == verify.LanguageVyper {
			return errors.New("unexpected Vyper fields")
		}
		return nil
	}
	if input.Language != "" && input.Language != verify.LanguageVyper {
		return errors.New("invalid Vyper language")
	}
	if input.TargetFile == "" || input.OptimizationRuns != nil || len(input.Libraries) > 0 || input.RuntimeEntrypoint != "" || input.CreationEntrypoint != "" || len(input.Contracts) > 0 {
		return errors.New("invalid Vyper verification fields")
	}
	if len(input.Input) > 0 && (len(input.Sources) > 0 || len(input.Interfaces) > 0 || input.OptimizationMode != "" || input.EVMVersion != "") {
		return errors.New("conflicting Vyper input formats")
	}
	return nil
}

func vyperMultipartSubmission(input verifierSubmission) *verify.VyperMultipartRequest {
	return &verify.VyperMultipartRequest{Sources: input.Sources, Interfaces: input.Interfaces, EVMVersion: input.EVMVersion, OptimizationMode: input.OptimizationMode}
}
