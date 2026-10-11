//go:build runtimee2e && hardhat3e2e && foundrye2e

package runtimee2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
)

func TestProductionTopologyMerge(t *testing.T) {
	root := repositoryRoot(t)
	dir := filepath.Join(root, ".local/topology-results")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected []string
	for _, e := range entries {
		actual = append(actual, e.Name())
	}
	for _, suite := range []string{"hardhat3", "foundry"} {
		for _, mode := range []string{"monolith", "distributed"} {
			expected = append(expected, suite+"-"+runtime.GOARCH+"-"+mode+".json")
		}
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatal("incomplete or extra topology evidence")
	}
	hardhat := checkTopologyPair[hardhatProxySnapshot](t, root, "hardhat3")
	foundry := checkTopologyPair[foundryVerificationSnapshot](t, root, "foundry")
	for _, record := range foundry {
		if string(record.Matrix) != "null" {
			t.Fatal("unexpected Foundry compiler matrix")
		}
	}
	evidence, err := mergeTopologyMatrices(hardhat)
	if err != nil {
		t.Fatal(err)
	}
	writeVyperAcceptanceEvidence(t, root, evidence)
}

func mergeTopologyMatrices(records []topologyEvidence) (vyperMatrixEvidence, error) {
	var result vyperMatrixEvidence
	if len(records) != 2 {
		return result, fmt.Errorf("missing topology matrix")
	}
	for i, record := range records {
		var evidence vyperMatrixEvidence
		if err := strictTopologyJSON(record.Matrix, &evidence); err != nil {
			return result, err
		}
		if !evidence.NativeLinux || evidence.ImageID != record.Image || len(evidence.Matrix) != 26 || len(evidence.Descriptors) != 26 || evidence.Monolith || evidence.Split {
			return result, fmt.Errorf("incomplete or misbound topology matrix")
		}
		if i == 0 {
			result = evidence
			continue
		}
		evidence.ImageID = result.ImageID
		if !reflect.DeepEqual(result, evidence) {
			return result, fmt.Errorf("different compiler or fixture identities between topologies")
		}
	}
	return result, nil
}

func TestTopologyEvidenceMatrices(t *testing.T) {
	evidence := vyperMatrixEvidence{NativeLinux: true, ImageID: "sha256:monolith", HostSHA256: "host", SharedSHA256: "shared", Descriptors: map[string]string{}, Matrix: map[string]vyperMatrixCase{}}
	for i := range 26 {
		key := fmt.Sprint(i)
		evidence.Descriptors[key] = "descriptor"
		evidence.Matrix[key] = vyperMatrixCase{Cases: 2, PackageSHA256: "package"}
	}
	records := func() []topologyEvidence {
		raw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		other := evidence
		other.ImageID = "sha256:distributed"
		next, err := json.Marshal(other)
		if err != nil {
			t.Fatal(err)
		}
		return []topologyEvidence{{Image: evidence.ImageID, Matrix: raw}, {Image: other.ImageID, Matrix: next}}
	}
	if _, err := mergeTopologyMatrices(records()); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"image_id", "native_linux", "host_sha256", "shared_sha256", "descriptors", "matrix", "monolith", "split"} {
		t.Run(field, func(t *testing.T) {
			pair := records()
			var changed map[string]any
			if err := json.Unmarshal(pair[1].Matrix, &changed); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "native_linux":
				changed[field] = false
			case "monolith", "split":
				changed[field] = true
			case "matrix", "descriptors":
				changed[field] = map[string]any{}
			default:
				changed[field] = "different"
			}
			var err error
			pair[1].Matrix, err = json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mergeTopologyMatrices(pair); err == nil {
				t.Fatal("accepted inconsistent topology matrix")
			}
		})
	}
	if _, err := mergeTopologyMatrices(records()[:1]); err == nil {
		t.Fatal("accepted missing matrix")
	}
}
