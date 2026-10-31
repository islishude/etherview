package billing

import (
	"strings"
	"testing"
)

func TestUsageOperationNamespace(t *testing.T) {
	for _, value := range []string{"etherview.account.balance", "etherview.blob.getblob"} {
		if !validUsageOperation(value) {
			t.Errorf("rejected current operation %q", value)
		}
	}
	for _, value := range []string{"etherscan.account.balance", "etherview.", "", "etherview." + strings.Repeat("x", 128)} {
		if validUsageOperation(value) {
			t.Errorf("accepted invalid operation %q", value)
		}
	}
}
