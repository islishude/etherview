package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type contract struct {
	Address    string `json:"address"`
	Name       string `json:"name"`
	Bundle     string `json:"bundle"`
	Entrypoint string `json:"entrypoint"`
	CodeHash   string `json:"codeHash"`
	CodeSHA256 string `json:"codeSha256"`
}

type inventory struct {
	ChainID   string            `json:"chainId"`
	Bundles   map[string]string `json:"bundles"`
	Contracts []contract        `json:"contracts"`
}

type genesis struct {
	Config struct {
		ChainID json.Number `json:"chainId"`
	} `json:"config"`
	Alloc map[string]struct {
		Code string `json:"code"`
	} `json:"alloc"`
}

type bundles map[string]map[string]json.RawMessage

const geasVersion = "0.3.3"

var bundleName = regexp.MustCompile(`^[A-Za-z0-9-]+\.json$`)

func loadInventory(directory, genesisFile string) (inventory, bundles, error) {
	var inv inventory
	var chain genesis
	if err := readJSON(genesisFile, &chain); err != nil {
		return inv, nil, err
	}
	if err := readJSON(filepath.Join(directory, "inventory.json"), &inv); err != nil {
		return inv, nil, err
	}
	if chain.Config.ChainID.String() != inv.ChainID {
		return inv, nil, errors.New("genesis chain mismatch")
	}
	accounts := make(map[string]string)
	for address, account := range chain.Alloc {
		if account.Code == "" || account.Code == "0x" {
			continue
		}
		address = "0x" + strings.ToLower(strings.TrimPrefix(address, "0x"))
		if _, exists := accounts[address]; exists {
			return inv, nil, errors.New("duplicate genesis address")
		}
		accounts[address] = account.Code
	}
	if len(accounts) != len(inv.Contracts) || len(accounts) == 0 {
		return inv, nil, errors.New("incomplete genesis verification inventory")
	}
	sources := make(bundles)
	for name, digest := range inv.Bundles {
		if !bundleName.MatchString(name) {
			return inv, nil, errors.New("invalid source bundle path")
		}
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return inv, nil, errors.New("cannot read source bundle")
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
			return inv, nil, fmt.Errorf("source bundle checksum mismatch: %s", name)
		}
		var bundle map[string]json.RawMessage
		if json.Unmarshal(data, &bundle) != nil || bundle == nil {
			return inv, nil, errors.New("invalid source bundle JSON")
		}
		sources[name] = bundle
	}
	seen := make(map[string]bool)
	for _, c := range inv.Contracts {
		code, found := accounts[c.Address]
		if seen[c.Address] || !found || sources[c.Bundle] == nil {
			return inv, nil, errors.New("invalid genesis inventory entry")
		}
		seen[c.Address] = true
		bytes, err := hex.DecodeString(strings.TrimPrefix(code, "0x"))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(bytes)) != c.CodeSHA256 {
			return inv, nil, fmt.Errorf("genesis code mismatch: %s", c.Name)
		}
	}
	return inv, sources, nil
}

func readJSON(file string, value any) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return errors.New("cannot read verification input")
	}
	if json.Unmarshal(data, value) != nil {
		return errors.New("invalid verification input JSON")
	}
	return nil
}

func submission(c contract, sources bundles) (map[string]any, error) {
	bundle := sources[c.Bundle]
	if c.Entrypoint != "" {
		return map[string]any{
			"language":           "geas",
			"compiler_version":   geasVersion,
			"input_kind":         "geas_sources",
			"runtime_entrypoint": c.Entrypoint,
			"sources":            bundle["sources"],
			"contract_name_hint": c.Name,
		}, nil
	}
	body := make(map[string]any)
	if _, ok := bundle["input_kind"]; ok {
		for key, value := range bundle {
			body[key] = value
		}
	} else {
		var compilation struct {
			Language string          `json:"language"`
			Version  string          `json:"compilerVersion"`
			Settings json.RawMessage `json:"compilerSettings"`
		}
		if json.Unmarshal(bundle["compilation"], &compilation) != nil || compilation.Language == "" || compilation.Version == "" {
			return nil, errors.New("invalid source compilation")
		}
		body["language"] = strings.ToLower(compilation.Language)
		body["compiler_version"] = compilation.Version
		body["input_kind"] = "standard_json"
		body["input"] = map[string]any{"language": compilation.Language, "sources": bundle["sources"], "settings": compilation.Settings}
	}
	body["contract_name_hint"] = c.Name
	return body, nil
}
