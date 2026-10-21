package router

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kimposoft/kimpo-plugin-sdk/gen/bizpb"
	formcontrollers "github.com/kimposoft/kimpo-form-controllers"
)

type manifestContract struct {
	Permissions struct {
		Network []string `json:"network"`
		Storage bool     `json:"storage"`
		Exec    bool     `json:"exec"`
	} `json:"permissions"`
	Capabilities struct {
		RuntimeKinds    []string `json:"runtime_kinds"`
		Directions      []string `json:"directions"`
		BackendServices []string `json:"backend_services"`
	} `json:"capabilities"`
	APIScope struct {
		AllowedRPCs []string `json:"allowed_rpcs"`
	} `json:"api_scope"`
	SchemaCompatibility map[string]json.RawMessage `json:"schema_compatibility"`
}

func readManifestContract(t *testing.T, path string) ([]byte, manifestContract) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest %s: %v", path, err)
	}
	var manifest manifestContract
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest %s: %v", path, err)
	}
	return raw, manifest
}

func assertRetiredActionQueryAbsent(t *testing.T, path string, manifest manifestContract) {
	t.Helper()
	for _, rpc := range manifest.APIScope.AllowedRPCs {
		if rpc == "kimpo.plugins.host.v1.ActionQueryService/SubmitExpression" {
			t.Fatalf("%s: retired ActionQuery RPC must not remain in allowed_rpcs: %v", path, manifest.APIScope.AllowedRPCs)
		}
	}
	if _, ok := manifest.SchemaCompatibility["host.actionquery"]; ok {
		t.Fatalf("%s: retired host.actionquery domain must not remain in schema_compatibility: %v", path, manifest.SchemaCompatibility)
	}
}

func TestManifestRouterAndFailureContract(t *testing.T) {
	rootRaw, manifest := readManifestContract(t, "../../manifest.json")
	embedRaw := formcontrollers.ManifestJSON()
	if !bytes.Equal(rootRaw, embedRaw) {
		t.Fatal("root package embedded manifest differs from tracked manifest.json")
	}
	assertRetiredActionQueryAbsent(t, "manifest.json", manifest)
	if _, err := os.Stat("../../cmd/plugin/manifest.json"); err == nil {
		t.Fatal("legacy cmd/plugin/manifest.json must be removed; the root manifest is the only authority")
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect legacy cmd/plugin/manifest.json: %v", err)
	}

	if len(manifest.Permissions.Network) != 0 || manifest.Permissions.Storage || manifest.Permissions.Exec {
		t.Fatalf("form controller permissions must remain closed: %+v", manifest.Permissions)
	}
	if strings.Join(manifest.Capabilities.RuntimeKinds, ",") != "action" ||
		strings.Join(manifest.Capabilities.Directions, ",") != "biz" ||
		strings.Join(manifest.Capabilities.BackendServices, ",") != "kimpo-form-controllers" {
		t.Fatalf("manifest capabilities drifted: %+v", manifest.Capabilities)
	}
	resp, err := New().Execute(context.Background(), &bizpb.ExecuteRequest{
		ActionName:     "ui.unknown",
		TriggerContext: &bizpb.TriggerContextMsg{Locale: "en-US"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSuccess() || resp.GetErrorCode() != "ACTION_UNKNOWN" || resp.GetMessageKey() != "action.unknown" ||
		resp.GetMessage() != "Unknown action: ui.unknown" || resp.GetResult() != nil {
		t.Fatalf("unknown action failure=%+v", resp)
	}
}

func TestPluginMainConsumesRootManifestAccessor(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "../../cmd/plugin/main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	const rootImportPath = "github.com/kimposoft/kimpo-form-controllers"
	rootAlias := ""
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if path != rootImportPath {
			continue
		}
		if spec.Name == nil {
			rootAlias = "formcontrollers"
		} else {
			rootAlias = spec.Name.Name
		}
	}
	if rootAlias == "" || rootAlias == "_" || rootAlias == "." {
		t.Fatalf("cmd/plugin/main.go must import root manifest package %q with a named accessor", rootImportPath)
	}

	manifestAssignmentCount := 0
	rootAssignmentCount := 0
	ast.Inspect(file, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		left, ok := assignment.Lhs[0].(*ast.SelectorExpr)
		if !ok || left.Sel.Name != "Manifest" {
			return true
		}
		receiver, ok := left.X.(*ast.Ident)
		if !ok || receiver.Name != "opts" {
			return true
		}
		manifestAssignmentCount++
		call, ok := assignment.Rhs[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return true
		}
		accessor, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || accessor.Sel.Name != "ManifestJSON" {
			return true
		}
		pkg, ok := accessor.X.(*ast.Ident)
		if ok && pkg.Name == rootAlias {
			rootAssignmentCount++
		}
		return true
	})
	if manifestAssignmentCount != 1 || rootAssignmentCount != 1 {
		t.Fatalf("cmd/plugin/main.go manifest assignments = %d, root ManifestJSON assignments = %d; want exactly 1/1",
			manifestAssignmentCount, rootAssignmentCount)
	}
}

func TestPluginSourceCannotUseRetiredHostQueryClient(t *testing.T) {
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Query" {
				t.Errorf("%s uses retired host Query client", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPluginSourceCannotBypassHostWithDatabaseOrHostInternalImports(t *testing.T) {
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if value == "database/sql" || strings.HasPrefix(value, "github.com/kimposoft/kimpo/") {
				t.Errorf("%s imports forbidden business-data bypass %q", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
