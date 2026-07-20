// Command plancheck validates Etherview's checked-in planning documents.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/islishude/etherview/internal/plancheck"
)

func main() {
	root := flag.String("root", ".", "repository root containing PLAN.md")
	flag.Parse()

	report := plancheck.Check(*root)
	if !report.OK() {
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintln(os.Stderr, diagnostic.String())
		}
		fmt.Fprintf(os.Stderr, "plan-check: failed with %d error(s)\n", len(report.Diagnostics))
		os.Exit(1)
	}

	fmt.Printf(
		"plan-check: ok (%d plans, %d work items, %d local links)\n",
		report.Plans,
		report.WorkItems,
		report.Links,
	)
}
