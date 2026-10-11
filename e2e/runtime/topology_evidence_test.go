//go:build runtimee2e

package runtimee2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Each CI topology runs on its own native host; only the collector can assert parity.
type topologyEvidence struct {
	Suite    string          `json:"suite"`
	Mode     string          `json:"mode"`
	Arch     string          `json:"arch"`
	Run      string          `json:"run"`
	Revision string          `json:"revision"`
	Image    string          `json:"image"`
	Client   string          `json:"client"`
	Matrix   json.RawMessage `json:"matrix"`
	Snapshot json.RawMessage `json:"snapshot"`
}

func topologyModes(t *testing.T) []string {
	t.Helper()
	modes, err := selectedTopologyModes(os.Getenv("RUNTIME_E2E_TOPOLOGY"))
	if err != nil {
		t.Fatal(err)
	}
	return modes
}

func selectedTopologyModes(mode string) ([]string, error) {
	switch mode {
	case "":
		return []string{"monolith", "distributed"}, nil
	case "monolith", "distributed":
		return []string{mode}, nil
	default:
		return nil, fmt.Errorf("invalid RUNTIME_E2E_TOPOLOGY %q", mode)
	}
}

func topologyIdentity(t *testing.T, suite, mode string) topologyEvidence {
	t.Helper()
	inspect := func(tag string) string {
		data, err := exec.CommandContext(t.Context(), dockerCommand(), "image", "inspect", "--format", "{{.Id}}", tag).Output()
		if err != nil {
			t.Fatal(err)
		}
		id := strings.TrimSpace(string(data))
		if !strings.HasPrefix(id, "sha256:") || len(id) != 71 {
			t.Fatal("invalid topology image identity")
		}
		return id
	}
	return topologyEvidence{Suite: suite, Mode: mode, Arch: runtime.GOARCH,
		Run: os.Getenv("GITHUB_RUN_ID"), Revision: os.Getenv("GITHUB_SHA"),
		Image:  inspect(valueOrDefault("IMAGE", "etherview:local")),
		Client: inspect(valueOrDefault("ETHERVIEW_"+strings.ToUpper(suite)+"_IMAGE", "etherview-"+suite+":local"))}
}

func writeTopologyEvidence(t *testing.T, root, suite, mode string, snapshot, matrix any) {
	t.Helper()
	evidence := topologyIdentity(t, suite, mode)
	if evidence.Run == "" || evidence.Revision == "" {
		t.Fatal("split topology evidence requires GITHUB_RUN_ID and GITHUB_SHA")
	}
	var err error
	evidence.Matrix, err = json.Marshal(matrix)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Snapshot, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".local/topology-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, suite+"-"+runtime.GOARCH+"-"+mode+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func strictTopologyJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing topology evidence")
	}
	return nil
}

func validateTopologyPair[T any](raw [][]byte, identity topologyEvidence) error {
	if len(raw) != 2 {
		return fmt.Errorf("need exactly two topology records")
	}
	var snapshots [2]T
	for i, mode := range []string{"monolith", "distributed"} {
		var evidence topologyEvidence
		if err := strictTopologyJSON(raw[i], &evidence); err != nil {
			return err
		}
		expected := identity
		expected.Mode = mode
		snapshot := evidence.Snapshot
		evidence.Snapshot, expected.Snapshot = nil, nil
		if !validTopologyDigest(evidence.Image) || !validTopologyDigest(evidence.Client) {
			return fmt.Errorf("invalid topology image digest")
		}
		// Each topology rebuilds its images. Retain both packaging IDs, but compare
		// source/run identity here and compiler/fixture identities in the collector.
		evidence.Image, evidence.Client, evidence.Matrix = "", "", nil
		expected.Image, expected.Client, expected.Matrix = "", "", nil
		if !reflect.DeepEqual(evidence, expected) {
			return fmt.Errorf("topology identity mismatch for %s", mode)
		}
		if len(snapshot) == 0 || string(snapshot) == "null" {
			return fmt.Errorf("missing topology snapshot")
		}
		if err := strictTopologyJSON(snapshot, &snapshots[i]); err != nil {
			return err
		}
		// Require every field, including zero-valued and nested fields, from the
		// typed snapshot. Missing fields must not silently become Go zero values.
		complete, err := json.Marshal(snapshots[i])
		if err != nil {
			return err
		}
		var originalShape, completeShape any
		if err := strictTopologyJSON(snapshot, &originalShape); err != nil {
			return err
		}
		if err := strictTopologyJSON(complete, &completeShape); err != nil {
			return err
		}
		if !reflect.DeepEqual(originalShape, completeShape) {
			return fmt.Errorf("incomplete topology snapshot")
		}
		var zero T
		if reflect.DeepEqual(snapshots[i], zero) {
			return fmt.Errorf("empty topology snapshot")
		}
	}
	if !reflect.DeepEqual(snapshots[0], snapshots[1]) {
		return fmt.Errorf("topology parity mismatch")
	}
	return nil
}

func validTopologyDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && !bytes.Equal(raw, make([]byte, 32))
}

func checkTopologyPair[T any](t *testing.T, root, suite string) []topologyEvidence {
	t.Helper()
	identity := topologyEvidence{Suite: suite, Arch: runtime.GOARCH, Run: os.Getenv("GITHUB_RUN_ID"), Revision: os.Getenv("GITHUB_SHA")}
	if identity.Run == "" || identity.Revision == "" {
		t.Fatal("topology merge requires run and revision")
	}
	var records [][]byte
	var evidence []topologyEvidence
	for _, mode := range []string{"monolith", "distributed"} {
		raw, err := os.ReadFile(filepath.Join(root, ".local/topology-results", suite+"-"+runtime.GOARCH+"-"+mode+".json"))
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, raw)
		var entry topologyEvidence
		if err := strictTopologyJSON(raw, &entry); err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, entry)
	}
	if err := validateTopologyPair[T](records, identity); err != nil {
		t.Fatal(err)
	}
	return evidence
}
