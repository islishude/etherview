// Command eventfixture regenerates independent Solidity indexed-event vectors.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/ethereum/go-ethereum/core/vm/runtime"
	"github.com/ethereum/go-ethereum/crypto"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	raw, err := exec.Command("node", "compiler/indexed-event-fixture.mjs").Output()
	if err != nil {
		return err
	}
	var fixture struct {
		Compiler string          `json:"compiler"`
		ABI      json.RawMessage `json:"abi"`
		Bytecode string          `json:"bytecode"`
		Cases    []struct {
			Name   string          `json:"name"`
			Values json.RawMessage `json:"values"`
			Topics []string        `json:"topics"`
			Data   string          `json:"data"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		return err
	}
	code, err := hex.DecodeString(fixture.Bytecode)
	if err != nil {
		return err
	}
	_, state, err := runtime.Execute(code, crypto.Keccak256([]byte("run()"))[:4], &runtime.Config{GasLimit: 30000000})
	if err != nil {
		return err
	}
	logs := state.Logs()
	if len(logs) != len(fixture.Cases) {
		return fmt.Errorf("got %d logs, want %d", len(logs), len(fixture.Cases))
	}
	for i, log := range logs {
		for _, topic := range log.Topics {
			fixture.Cases[i].Topics = append(fixture.Cases[i].Topics, topic.Hex())
		}
		fixture.Cases[i].Data = "0x" + hex.EncodeToString(log.Data)
	}
	output, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	path := "web/src/contracts/fixtures/indexed-events.json"
	if err = os.WriteFile(path, append(output, '\n'), 0644); err != nil {
		return err
	}
	format := exec.Command("pnpm", "exec", "oxfmt", "--write", path)
	format.Stdout, format.Stderr = os.Stdout, os.Stderr
	return format.Run()
}
