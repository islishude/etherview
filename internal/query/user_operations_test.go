package query

import (
	"encoding/json"
	"testing"

	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/browsing"
	"github.com/islishude/etherview/internal/publicquery"
)

func TestEmptyUserOperationPagesPreserveArrayAndSnapshot(t *testing.T) {
	t.Parallel()
	for name, build := range map[string]func([]gen.UserOperationSummary, []userOperationCursor, userOperationCursor, int) (publicquery.UserOperationPage, error){
		"global and address": userOperationPage,
		"transaction":        transactionUserOperationPage,
	} {
		for _, state := range []string{"complete", "partial", "pending"} {
			t.Run(name+"/"+state, func(t *testing.T) {
				status := &browsing.Status{State: state, Revision: "9007199254740993"}
				cursor := userOperationCursor{IndexStart: 10, SnapshotNumber: 20, Browsing: status}
				page, err := build(nil, nil, cursor, 25)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(page.Items)
				if err != nil || string(encoded) != "[]" {
					t.Fatalf("empty items encode as %s, err=%v", encoded, err)
				}
				if page.NextCursor != "" || page.CoverageStart != 10 || page.CoverageEnd != 20 || page.Browsing != status {
					t.Fatalf("empty page lost snapshot metadata: %+v", page)
				}
			})
		}
	}
}
