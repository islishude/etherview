// Command bundle builds the same application-locked package used by workers.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	vyper "github.com/islishude/etherview/compiler/vyper"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	cache := flag.String("cache", ".local/vyper-wasm/downloads", "authenticated upstream cache")
	output := flag.String("output", ".local/vyper-wasm/packages", "package output directory")
	version := flag.String("version", "", "one compiler version")
	update := flag.String("update-lock", "", "explicitly regenerate lock output after review")
	flag.Parse()
	recipes, err := vyper.Recipes()
	if err != nil {
		return err
	}
	if *version != "" {
		if _, err := vyper.Lookup(*version); err != nil {
			return err
		}
	}
	for i, r := range recipes {
		if *version != "" && r.Version != *version {
			continue
		}
		raw, manifest, err := vyper.Build(context.Background(), r, func(_ context.Context, a vyper.Archive) ([]byte, error) {
			return os.ReadFile(filepath.Join(*cache, a.SHA256))
		})
		if err != nil {
			return err
		}
		var compact bytes.Buffer
		if err = json.Compact(&compact, r.Manifest); err != nil {
			return err
		}
		if *update == "" && !bytes.Equal(bytes.TrimSpace(manifest), compact.Bytes()) {
			return fmt.Errorf("locked package mismatch: %s", r.Version)
		}
		recipes[i].Manifest = json.RawMessage(bytes.TrimSpace(manifest))
		dir := filepath.Join(*output, r.Version)
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for name, data := range map[string][]byte{"packages.zip": raw, "package-manifest.json": manifest} {
			if err = os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				return err
			}
		}
	}
	if *update != "" {
		raw, err := json.MarshalIndent(vyper.Lock{Schema: "etherview-vyper-lock-v1", Recipes: recipes}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(*update, append(raw, '\n'), 0o644)
	}
	return nil
}
