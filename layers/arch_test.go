package layers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Gate G7a: non-test files import only from this list; test files may
// additionally use the second list.
var allowedImports = map[string]bool{
	"bytes":         true,
	"encoding/json": true,
	"errors":        true,
	"fmt":           true,
	"regexp":        true,
	"sort":          true,
	"strings":       true,
}

var allowedTestImports = map[string]bool{
	"embed":         true,
	"testing":       true,
	"reflect":       true,
	"go/ast":        true,
	"go/parser":     true,
	"go/token":      true,
	"os":            true,
	"path/filepath": true,
	"io/fs":         true,
	"strings":       true,
}

func packageGoFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("no Go files found")
	}
	return files
}

func TestArchImports(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range packageGoFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		isTest := strings.HasSuffix(name, "_test.go")
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if allowedImports[path] {
				continue
			}
			if isTest && allowedTestImports[path] {
				continue
			}
			t.Errorf("%s imports %q, which is not on the allowed list", name, path)
		}
	}
}

func TestArchGoModHasNoRequires(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "require") {
			t.Fatalf("go.mod contains a require line: %q", line)
		}
	}
}

// Gate G7c: the package's exported identifiers are exactly the surface of the
// build spec, enumerated via go/ast against this allowlist.
var exportedSurface = []string{
	"type Layer",
	"field Layer.SchemaURI",
	"field Layer.TypeName",
	"field Layer.Namespace",
	"field Layer.Subject",
	"field Layer.Predicate",
	"field Layer.Version",
	"field Layer.Fields",
	"field Layer.Required",
	"field Layer.Lineage",
	"type Catalog",
	"method Catalog.BySchemaURI",
	"method Catalog.ByType",
	"type LodgeRefusal",
	"field LodgeRefusal.Kind",
	"field LodgeRefusal.Evidence",
	"type MemoryCatalog",
	"func NewMemoryCatalog",
	"method MemoryCatalog.Lodge",
	"method MemoryCatalog.BySchemaURI",
	"method MemoryCatalog.ByType",
	"type Arrival",
	"field Arrival.Type",
	"field Arrival.InheritsPresent",
	"field Arrival.InheritsWellFormed",
	"field Arrival.Inherits",
	"field Arrival.Content",
	"field Arrival.CustomData",
	"field Arrival.CustomDataContentType",
	"func ParseArrival",
	"type Refusal",
	"field Refusal.Kind",
	"field Refusal.Field",
	"field Refusal.Layer",
	"field Refusal.Evidence",
	"type Minted",
	"field Minted.SchemaURI",
	"field Minted.TypeName",
	"field Minted.Content",
	"field Minted.CustomData",
	"field Minted.CustomDataContentType",
	"type Resolution",
	"field Resolution.Minted",
	"method Resolution.ServeAt",
	"type Outcome",
	"field Outcome.Kind",
	"field Outcome.Resolution",
	"field Outcome.Refusal",
	"field Outcome.Fault",
	"func Decompress",
}

func TestArchExportedSurface(t *testing.T) {
	fset := token.NewFileSet()
	got := map[string]bool{}
	for _, name := range packageGoFiles(t) {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					got["func "+d.Name.Name] = true
					continue
				}
				recv := receiverTypeName(d.Recv.List[0].Type)
				if ast.IsExported(recv) {
					got["method "+recv+"."+d.Name.Name] = true
				} else {
					got["method (unexported "+recv+")."+d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if !s.Name.IsExported() {
							continue
						}
						got["type "+s.Name.Name] = true
						switch tt := s.Type.(type) {
						case *ast.StructType:
							for _, field := range tt.Fields.List {
								for _, n := range field.Names {
									if n.IsExported() {
										got["field "+s.Name.Name+"."+n.Name] = true
									}
								}
							}
						case *ast.InterfaceType:
							for _, m := range tt.Methods.List {
								for _, n := range m.Names {
									got["method "+s.Name.Name+"."+n.Name] = true
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								got["value "+n.Name] = true
							}
						}
					}
				}
			}
		}
	}

	want := map[string]bool{}
	for _, id := range exportedSurface {
		want[id] = true
	}
	for id := range got {
		if !want[id] {
			t.Errorf("exported but not in the specified surface: %s", id)
		}
	}
	for id := range want {
		if !got[id] {
			t.Errorf("in the specified surface but not exported: %s", id)
		}
	}
}

func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}

// Gate G8: the forbidden word (assembled here so this file cannot trip its
// own check) appears nowhere in the repository. The scope is the repository
// layout of the build spec: LICENSE, README.md, go.mod, and everything under
// layers/.
func TestVocabulary(t *testing.T) {
	forbidden := "s" + "lot"
	check := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Errorf("%s contains the forbidden word %q", path, forbidden)
		}
	}
	for _, path := range []string{"../LICENSE", "../README.md", "../go.mod"} {
		check(path)
	}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			check(path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
