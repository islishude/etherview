package enssync

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestENSHistoryTextEvidenceAndIndexedKeys(t *testing.T) {
	p, e := loadProfile("resolver")
	if e != nil {
		t.Fatal(e)
	}
	event := p.abi.Events["TextChanged"]
	node := nameHash("alice.eth")
	data, e := event.Inputs.NonIndexed().Pack("description", strings.Repeat("名称", 2000))
	if e != nil {
		t.Fatal(e)
	}
	log := types.Log{Topics: []common.Hash{event.ID, node, crypto.Keccak256Hash([]byte("description"))}, Data: data}
	values, e := unpackEvent(event, log)
	if e != nil {
		t.Fatal(e)
	}
	fields, e := historyFields(event, values)
	if e != nil {
		t.Fatal(e)
	}
	if fields[1].Type != "hash" || fields[1].Value != log.Topics[2].Hex() || !fields[3].Truncated || !utf8.ValidString(fields[3].Value) || len(fields[3].Value) > 4096 {
		t.Fatalf("invalid disclosure %+v", fields)
	}
	values["key"] = "different"
	if _, e = historyFields(event, values); e != ErrInvalidEvent {
		t.Fatalf("mismatched key accepted: %v", e)
	}
}
func TestENSHistoryProtocolActionCatalog(t *testing.T) {
	for _, profile := range []string{"registry", "registrar", "controller", "wrapper", "resolver", "v2_registry", "v2_resolver"} {
		p, e := loadProfile(profile)
		if e != nil {
			t.Fatal(e)
		}
		for _, event := range p.abi.Events {
			if strings.HasPrefix(event.RawName, "Text") || strings.HasPrefix(event.RawName, "Contenthash") || event.RawName == "Approval" || event.RawName == "LabelUnregistered" {
				if historyAction(event.RawName) == "" {
					t.Fatal("missing history", profile, event.RawName)
				}
			}
		}
	}
	// Resolver EAC resources are setter arguments, not record IDs. They must
	// never be joined to recordId, even if the numeric values happen to match.
	c := &Collector{}
	nodes, pending, e := c.historySubjects(t.Context(), nil, 1, Deployment{Profile: "v2_resolver"}, types.Log{}, 0, "EACRolesChanged", map[string]any{"resource": uint64(1)})
	if e != nil || pending || len(nodes) != 0 {
		t.Fatal(nodes, pending, e)
	}
}
