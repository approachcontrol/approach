package flowstore_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryNonTestNewStoreCallSiteNamesARole is the guard that keeps role
// separation from being silently inherited.
//
// The zero value of Role is RoleMigrator, which is exactly what the hundred-odd
// bare StoreOptions literals in tests need — and exactly what would let a new
// production caller migrate by accident. Grep cannot answer this reliably (a
// caller in model/ was missed once by hand), so this walks the module and reads
// the composite literal.
func TestEveryNonTestNewStoreCallSiteNamesARole(t *testing.T) {
	root := moduleRoot(t)
	var missing []string
	walkNonTestGoFiles(t, root, func(path string, file *ast.File, fset *token.FileSet) {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isFlowstoreNewStore(call.Fun) || len(call.Args) != 1 {
				return true
			}
			literal, ok := call.Args[0].(*ast.CompositeLit)
			if !ok {
				missing = append(missing, position(fset, call.Pos(), root)+
					" (argument is not a StoreOptions literal, so the role cannot be read)")
				return true
			}
			if !literalHasKey(literal, "Role") {
				missing = append(missing, position(fset, call.Pos(), root))
			}
			return true
		})
	})
	if len(missing) > 0 {
		t.Fatalf("flowstore.NewStore call sites without an explicit Role:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

func isFlowstoreNewStore(fun ast.Expr) bool {
	selector, ok := fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewStore" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "flowstore"
}

func literalHasKey(literal *ast.CompositeLit, key string) bool {
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if ident, ok := keyValue.Key.(*ast.Ident); ok && ident.Name == key {
			return true
		}
	}
	return false
}

func position(fset *token.FileSet, pos token.Pos, root string) string {
	at := fset.Position(pos)
	if relative, err := filepath.Rel(root, at.Filename); err == nil {
		return relative + ":" + itoa(at.Line)
	}
	return at.String()
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// moduleRoot finds the repository root. A package test runs with the package
// directory as its working directory, so the module root is up the tree.
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

// walkNonTestGoFiles visits every .go file in the module that is not a _test.go
// file. web/ and node_modules/ hold no Go, and .git holds no source at all.
func walkNonTestGoFiles(t *testing.T, root string, visit func(string, *ast.File, *token.FileSet)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "web", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		visit(path, file, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
}
