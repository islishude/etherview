package plancheck

import (
	"path/filepath"
	"testing"
)

func TestRepositoryPlans(t *testing.T) {
	t.Parallel()

	report := Check(filepath.Join("..", ".."))
	for _, diagnostic := range report.Diagnostics {
		t.Error(diagnostic.String())
	}
	t.Logf("checked %d plans, %d work items, %d local links", report.Plans, report.WorkItems, report.Links)
}
