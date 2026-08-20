package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// staticErrorfPackages is the issue 277 #33 inventory (updated paths).
var staticErrorfPackages = []string{
	"cmd/approach",
	"flowstore",
	"planstore",
	"config",
	"agent",
	"embeddedterm",
}

func TestStaticErrorfUsesErrorsNew(t *testing.T) {
	root := moduleRoot(t)
	var hits []string
	fset := token.NewFileSet()
	for _, pkg := range staticErrorfPackages {
		dir := filepath.Join(root, pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", pkg, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !isFmtErrorf(call.Fun) || len(call.Args) == 0 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				format, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", fset.Position(lit.Pos()), err)
				}
				if strings.Contains(format, "%") {
					return true
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					rel = path
				}
				hits = append(hits, rel+":"+strconv.Itoa(fset.Position(lit.Pos()).Line)+" "+lit.Value)
				return true
			})
		}
	}
	if len(hits) > 0 {
		t.Fatalf("fmt.Errorf with a verb-free message must be errors.New:\n  %s", strings.Join(hits, "\n  "))
	}
}

func isFmtErrorf(fun ast.Expr) bool {
	selector, ok := fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Errorf" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "fmt"
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
