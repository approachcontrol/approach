package launchcontrol

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The package's whole reason to exist is that it closes the dependency cycle
// the annex could not. Any module import beyond these three reopens it.
func TestPackageImportsStayInsideTheContract(t *testing.T) {
	allowed := map[string]bool{
		"github.com/approachcontrol/approach/flowstore":          true,
		"github.com/approachcontrol/approach/internal/artifacts": true,
		"github.com/approachcontrol/approach/internal/flowlease": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/approachcontrol/approach/") {
				continue
			}
			if !allowed[path] {
				t.Errorf("%s imports %s; launchcontrol may import only flowstore, internal/artifacts, internal/flowlease", name, path)
			}
		}
	}
}
