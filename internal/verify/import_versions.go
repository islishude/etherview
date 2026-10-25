package verify

import "regexp"

// Provider commit labels only select a candidate release. Compiler evidence
// always comes from the application lock and local bytecode verification.
var importedVyperVersionPattern = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)(?:\+commit\.[0-9a-fA-F]{7,64})?$`)

func importedVyperReleaseVersion(value string) (string, bool) {
	version := importedVyperVersionPattern.FindStringSubmatch(value)
	if len(version) != 2 || !stableVyperVersion(version[1]) {
		return "", false
	}
	return version[1], true
}
