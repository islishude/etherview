// Command wasmpack seals an assembled compiler runtime at build time.
package main

import (
	"fmt"
	"github.com/islishude/etherview/internal/compilerbundle"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: wasmpack ROOT EXECUTABLE_BASENAME")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "--image-check":
		err = checkImage(os.Args[2])
	case "--check":
		_, err = compilerbundle.Validate(os.Args[2])
	default:
		err = compilerbundle.Seal(os.Args[1], os.Args[2])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
