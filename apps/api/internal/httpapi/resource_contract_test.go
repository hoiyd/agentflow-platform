package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

type resourceContract struct {
	spec     map[string]any
	compiler *jsonschema.Compiler
}

func loadResourceContract(t *testing.T) resourceContract {
	t.Helper()
	data, err := os.ReadFile("../../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://agentflow.test/openapi", spec); err != nil {
		t.Fatal(err)
	}
	return resourceContract{spec, compiler}
}

func contractPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func (c resourceContract) validate(t *testing.T, pointer string, body []byte) {
	t.Helper()
	schema, err := c.compiler.Compile("https://agentflow.test/openapi#" + pointer)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("%s: serialized body violates contract: %v", pointer, err)
	}
}

func (c resourceContract) response(t *testing.T, method, route string, response *httptest.ResponseRecorder) {
	t.Helper()
	paths := c.spec["paths"].(map[string]any)
	path, ok := paths[route].(map[string]any)
	if !ok {
		t.Fatalf("missing contract path %s", route)
	}
	operation, ok := path[strings.ToLower(method)].(map[string]any)
	if !ok {
		t.Fatalf("missing contract operation %s %s", method, route)
	}
	status := strconv.Itoa(response.Code)
	replies := operation["responses"].(map[string]any)
	reply, ok := replies[status].(map[string]any)
	if !ok {
		t.Fatalf("missing %s %s response %s", method, route, status)
	}
	if response.Code == 204 {
		if response.Body.Len() != 0 || reply["content"] != nil {
			t.Fatal("204 must have no response body")
		}
		return
	}
	pointer := "/paths/" + contractPointer(route) + "/" + strings.ToLower(method) + "/responses/" + status
	if ref, ok := reply["$ref"].(string); ok {
		pointer = strings.TrimPrefix(ref, "#")
	}
	c.validate(t, pointer+"/content/application~1json/schema", response.Body.Bytes())
}

// Covers literal production registrations, including conditionally enabled routes.
// It does not infer dynamically constructed paths or validate handler behavior.
func TestHTTPRouteContractCoverage(t *testing.T) {
	contract := loadResourceContract(t)
	paths := contract.spec["paths"].(map[string]any)
	normalize := func(path string) string {
		parts := strings.Split(path, "/")
		for i, part := range parts {
			if strings.HasPrefix(part, "{") {
				parts[i] = "{}"
			}
		}
		return strings.Join(parts, "/")
	}
	covered := map[string]bool{}
	for path, definition := range paths {
		for method := range definition.(map[string]any) {
			covered[strings.ToUpper(method)+" "+normalize(path)] = true
		}
	}
	checked := 0
	for _, dir := range []string{".", "../identity"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, filename := range files {
			if strings.HasSuffix(filename, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "HandleFunc" {
					return true
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				pattern, _ := strconv.Unquote(literal.Value)
				method, path, ok := strings.Cut(pattern, " ")
				if ok {
					checked++
					if !covered[method+" "+normalize(path)] {
						t.Errorf("%s: missing contract for %s", filename, pattern)
					}
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no production HTTP registrations inspected")
	}
	t.Logf("checked %d literal production HTTP operations", checked)
}
