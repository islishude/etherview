// Command sourcecheck enforces production source ownership boundaries.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/islishude/etherview/internal/sourcecheck"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	report := sourcecheck.Check(*root)
	if !report.OK() {
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(os.Stderr, diagnostic.String())
		}
		fmt.Fprintf(os.Stderr, "source-check: failed with %d error(s)\n", len(report.Diagnostics))
		os.Exit(1)
	}
	fmt.Printf("source-check: ok (%d Go files, %d SQL files)\n", report.GoFiles, report.SQLFiles)
}
