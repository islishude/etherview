// Command etherview-wasm is the fixed compiler subprocess owned by API/all.
package main

import (
	"context"
	"fmt"
	"github.com/islishude/etherview/internal/wasmcompiler"
	"os"
)

func main() {
	if !wasmcompiler.LinkedRuntimeMatches() {
		fmt.Fprintln(os.Stderr, "WASM runtime identity invalid")
		os.Exit(1)
	}
	if err := wasmcompiler.ApplyProcessLimits(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := wasmcompiler.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
