package formcontrollers

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestManifestJSONMatchesTrackedRootAndRejectsRetiredActionQuery(t *testing.T) {
	want, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	got := ManifestJSON()
	if !bytes.Equal(got, want) {
		t.Fatal("embedded manifest differs from tracked root manifest.json")
	}
	if len(got) == 0 {
		t.Fatal("embedded manifest must not be empty")
	}
	got[0] ^= 0xff
	if fresh := ManifestJSON(); !bytes.Equal(fresh, want) {
		t.Fatal("ManifestJSON must return an isolated copy")
	}

	var manifest struct {
		APIScope struct {
			AllowedRPCs []string `json:"allowed_rpcs"`
		} `json:"api_scope"`
		SchemaCompatibility map[string]json.RawMessage `json:"schema_compatibility"`
	}
	if err := json.Unmarshal(want, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, rpc := range manifest.APIScope.AllowedRPCs {
		if rpc == "kimpo.plugins.host.v1.ActionQueryService/SubmitExpression" {
			t.Fatalf("retired ActionQuery RPC remains in root manifest: %v", manifest.APIScope.AllowedRPCs)
		}
	}
	if _, ok := manifest.SchemaCompatibility["host.actionquery"]; ok {
		t.Fatalf("retired host.actionquery remains in root manifest: %v", manifest.SchemaCompatibility)
	}
}
