package main

import (
	"os"

	"github.com/islishude/etherview/internal/geascompiler"
)

func main() {
	os.Exit(geascompiler.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
