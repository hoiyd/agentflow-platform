package config

import (
	"bufio"
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
)

// Failure inventory: missing reader/route credentials and duplicate template
// names must fail without reading local .env files or printing credential values.
func TestEnvironmentTemplateCoversBackendReaders(t *testing.T) {
	root := filepath.Join("..", "..")
	readTemplate := func(path string) map[string]bool {
		t.Helper()
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		keys := map[string]bool{}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, _, ok := strings.Cut(line, "=")
			if !ok || keys[name] {
				t.Fatalf("invalid or duplicate template name: %s", name)
			}
			keys[name] = true
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	apiKeys := readTemplate(filepath.Join(root, ".env.example"))
	scriptKeys := readTemplate(filepath.Join(root, "..", "..", "scripts", ".env.example"))
	require := func(name, owner string) {
		t.Helper()
		keys := apiKeys
		for _, consumer := range []string{"cmd/eval/", "internal/runclient/", "internal/testsupport/"} {
			if strings.Contains(filepath.ToSlash(owner), consumer) {
				keys = scriptKeys
			}
		}
		if name != "HOME" && !keys[name] {
			t.Errorf(".env.example missing %s read by %s", name, owner)
		}
	}
	for _, directory := range []string{"app", "cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(source, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				reader := false
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					reader = strings.HasPrefix(fn.Name, "get") && strings.HasSuffix(fn.Name, "Env") || fn.Name == "environment"
				case *ast.SelectorExpr:
					owner, ok := fn.X.(*ast.Ident)
					reader = ok && (owner.Name == "os" && (fn.Sel.Name == "Getenv" || fn.Sel.Name == "LookupEnv") || owner.Name == "credential" && fn.Sel.Name == "FromEnvironment")
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				if reader && ok && literal.Kind == token.STRING {
					name, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatal(err)
					}
					require(name, path)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	paths, err := filepath.Glob(filepath.Join(root, "config", "model-routes*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Routes []struct {
				CredentialEnvironment string `json:"credential_environment"`
			} `json:"routes"`
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		for _, route := range catalog.Routes {
			if route.CredentialEnvironment != "" {
				require(route.CredentialEnvironment, path)
			}
		}
	}
}
