package etherscan

import (
	"encoding/json"
	"net/url"
	"path"
	"strings"

	"github.com/islishude/etherview/internal/verify"
)

func parseVyperVerificationForm(values url.Values, source, target, version string, maximum int) (etherscanVerificationForm, error) {
	invalid := invalidParameter("invalid Vyper verification input")
	version = strings.TrimPrefix(version, "vyper:")
	if version != verify.VyperCompilerVersion {
		return etherscanVerificationForm{}, invalid
	}
	filename, name := target, ""
	separator := strings.LastIndex(target, ":")
	qualified := separator >= 0
	if qualified {
		filename, name = target[:separator], target[separator+1:]
	}
	if qualified && name != strings.TrimSuffix(path.Base(filename), ".vy") {
		return etherscanVerificationForm{}, invalid
	}
	for key, entries := range values {
		if key == "runs" || strings.HasPrefix(key, "libraryname") || strings.HasPrefix(key, "libraryaddress") {
			for _, entry := range entries {
				if entry != "" {
					return etherscanVerificationForm{}, invalid
				}
			}
		}
	}
	prepared, err := verify.PrepareVyperStandardJSON(json.RawMessage(source), filename, maximum)
	if err != nil {
		return etherscanVerificationForm{}, invalid
	}
	var document struct {
		Settings struct {
			Optimize   string `json:"optimize"`
			EVMVersion string `json:"evmVersion"`
		} `json:"settings"`
	}
	if json.Unmarshal(prepared, &document) != nil {
		return etherscanVerificationForm{}, invalid
	}
	used, err := oneVerificationValue(values, "optimizationUsed", false)
	if err != nil {
		return etherscanVerificationForm{}, err
	}
	if used != "" && ((used != "0" && used != "1") || (used == "0") != (document.Settings.Optimize == "none")) {
		return etherscanVerificationForm{}, invalid
	}
	evm, err := oneVerificationValue(values, "evmversion", false)
	if err != nil {
		return etherscanVerificationForm{}, err
	}
	if evm != "" && evm != "default" && evm != "Default" && evm != document.Settings.EVMVersion {
		return etherscanVerificationForm{}, invalid
	}
	return etherscanVerificationForm{language: verify.LanguageVyper, compilerVersion: version, targetFile: filename, contractIdentifier: filename + ":" + strings.TrimSuffix(path.Base(filename), ".vy"), standardJSON: prepared}, nil
}
