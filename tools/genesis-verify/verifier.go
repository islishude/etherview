package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

type artifact struct {
	Kind       string `json:"kind"`
	Resolution string `json:"resolution"`
	Target     struct {
		ChainID  string `json:"chain_id"`
		Address  string `json:"address"`
		CodeHash string `json:"code_hash"`
	} `json:"target"`
	RuntimeMatch *struct {
		MatchType string `json:"match_type"`
	} `json:"runtime_match"`
	CreationMatch json.RawMessage `json:"creation_match"`
}

func published(a artifact, c contract, chainID string) bool {
	return a.Kind == "verification_success" && a.Resolution == "exact_address" && a.Target.ChainID == chainID && strings.ToLower(a.Target.Address) == c.Address && a.Target.CodeHash == c.CodeHash && a.RuntimeMatch != nil && (a.RuntimeMatch.MatchType == "full" || a.RuntimeMatch.MatchType == "partial")
}

type verifier struct {
	inventory inventory
	bundles   bundles
	request   func(context.Context, string, any) (response, error)
	rpc       func(context.Context, string, []any) (json.RawMessage, error)
	wait      func(context.Context) error
	timeout   time.Duration
	log       func(string, ...any)
}

func wait(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (v *verifier) verifyAll(ctx context.Context) error {
	chain, err := v.rpc(ctx, "eth_chainId", []any{})
	if err != nil {
		return err
	}
	var chainHex string
	if json.Unmarshal(chain, &chainHex) != nil || !strings.HasPrefix(chainHex, "0x") {
		return errors.New("RPC chain mismatch")
	}
	chainID, ok := new(big.Int).SetString(chainHex[2:], 16)
	if !ok || chainID.String() != v.inventory.ChainID {
		return errors.New("RPC chain mismatch")
	}
	block, err := v.rpc(ctx, "eth_getBlockByNumber", []any{"0x0", false})
	if err != nil {
		return err
	}
	var genesisBlock struct {
		Hash string `json:"hash"`
	}
	if json.Unmarshal(block, &genesisBlock) != nil || genesisBlock.Hash == "" {
		return errors.New("invalid genesis RPC block")
	}
	if err = v.verifyImport(ctx, genesisBlock.Hash); err != nil {
		return err
	}
	reused := 0
	for _, c := range v.inventory.Contracts {
		prior, err := v.request(ctx, "/api/v1/contracts/"+c.Address+"/verification", nil)
		if err != nil {
			return err
		}
		if prior.Status == 200 && isPublished(prior, c, v.inventory.ChainID) {
			reused++
			v.log("genesis-verify: %s already verified", c.Name)
			continue
		}
		if prior.Status != 200 && prior.Status != 404 {
			return fmt.Errorf("%s: artifact HTTP %d", c.Name, prior.Status)
		}
		if err = v.verifyContract(ctx, c); err != nil {
			return err
		}
		v.log("genesis-verify: %s verified", c.Name)
	}
	v.log("genesis-verify: %d/%d contracts verified (%d reused)", len(v.inventory.Contracts), len(v.inventory.Contracts), reused)
	return nil
}

func isPublished(res response, c contract, chainID string) bool {
	var envelope struct {
		Data artifact `json:"data"`
	}
	return json.Unmarshal(res.Body, &envelope) == nil && published(envelope.Data, c, chainID)
}

func (v *verifier) verifyImport(parent context.Context, blockHash string) error {
	ctx, cancel := context.WithTimeout(parent, v.timeout)
	defer cancel()
	for ctx.Err() == nil {
		res, err := v.request(ctx, "/api/v1/genesis/accounts?limit=100", nil)
		if err != nil {
			return err
		}
		if res.Status == 200 {
			return v.validateImport(res, blockHash)
		}
		if res.Status != 503 {
			return fmt.Errorf("genesis import HTTP %d", res.Status)
		}
		if v.wait(ctx) != nil {
			break
		}
	}
	return errors.New("genesis import timeout")
}

func (v *verifier) validateImport(res response, blockHash string) error {
	var imported struct {
		Data []struct {
			Type      string `json:"type"`
			Address   string `json:"address"`
			CodeHash  string `json:"code_hash"`
			BlockHash string `json:"block_hash"`
		} `json:"data"`
		Meta struct {
			ChainID    string `json:"chain_id"`
			NextCursor string `json:"next_cursor"`
		} `json:"meta"`
	}
	if json.Unmarshal(res.Body, &imported) != nil {
		return errors.New("invalid genesis import response")
	}
	accounts := make(map[string]struct{ codeHash, blockHash string })
	for _, a := range imported.Data {
		if a.Type != "contract" {
			continue
		}
		address := strings.ToLower(a.Address)
		if _, exists := accounts[address]; exists {
			return errors.New("imported genesis inventory mismatch")
		}
		accounts[address] = struct{ codeHash, blockHash string }{a.CodeHash, a.BlockHash}
	}
	if imported.Meta.ChainID != v.inventory.ChainID || imported.Meta.NextCursor != "" || len(accounts) != len(v.inventory.Contracts) {
		return errors.New("imported genesis inventory mismatch")
	}
	for _, c := range v.inventory.Contracts {
		account, ok := accounts[c.Address]
		if !ok || account.codeHash != c.CodeHash || account.blockHash != blockHash {
			return fmt.Errorf("imported genesis identity mismatch: %s", c.Name)
		}
	}
	return nil
}

var jobID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (v *verifier) verifyContract(parent context.Context, c contract) error {
	ctx, cancel := context.WithTimeout(parent, v.timeout)
	defer cancel()
	path := "/api/v1/contracts/" + c.Address + "/verification"
	body, err := submission(c, v.bundles)
	if err != nil {
		return err
	}
	id, err := v.submit(ctx, path, body)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	if err = v.poll(ctx, id); err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	res, err := v.request(ctx, path, nil)
	if err != nil {
		return err
	}
	if res.Status != 200 || !isPublished(res, c, v.inventory.ChainID) {
		return fmt.Errorf("%s: published artifact mismatch", c.Name)
	}
	return nil
}

func (v *verifier) submit(ctx context.Context, path string, body any) (string, error) {
	for ctx.Err() == nil {
		res, err := v.request(ctx, path, body)
		if err != nil {
			return "", err
		}
		if res.Status == 202 {
			var accepted struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(res.Body, &accepted) != nil || !jobID.MatchString(accepted.Data.ID) {
				return "", errors.New("invalid verification job")
			}
			return accepted.Data.ID, nil
		}
		if res.Status != 429 && res.Status != 503 {
			return "", fmt.Errorf("submit HTTP %d", res.Status)
		}
		if v.wait(ctx) != nil {
			break
		}
	}
	return "", errors.New("submission timeout")
}

func (v *verifier) poll(ctx context.Context, id string) error {
	for ctx.Err() == nil {
		res, err := v.request(ctx, "/api/v1/verifier/jobs/"+id, nil)
		if err != nil {
			return err
		}
		if res.Status != 200 {
			return fmt.Errorf("job HTTP %d", res.Status)
		}
		var result struct {
			Data struct {
				Status  string   `json:"status"`
				Outcome artifact `json:"outcome"`
			} `json:"data"`
		}
		if json.Unmarshal(res.Body, &result) != nil {
			return errors.New("invalid job response")
		}
		job := result.Data
		switch job.Status {
		case "failed", "cancelled":
			return fmt.Errorf("verification %s", job.Status)
		case "succeeded":
			if job.Outcome.Kind != "verification_success" || job.Outcome.RuntimeMatch == nil || present(job.Outcome.CreationMatch) {
				return errors.New("invalid genesis match evidence")
			}
			return nil
		case "queued", "running":
		default:
			return errors.New("invalid job status")
		}
		if v.wait(ctx) != nil {
			break
		}
	}
	return errors.New("verification timeout")
}
