package model

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	flowOccupancyLaunchOwners  = "launch ownership map"
	flowOccupancySessionOwners = "saved-session owner index"
	flowOccupancyTerminalSlots = "retained embedded terminal slots"
	flowOccupancyLease         = "Flow lease inspector"
	flowOccupancyFlowSource    = "authoritative Flow read"
	flowOccupancySessionSource = "authoritative session reads"
)

type flowOccupancyAllowanceKey struct {
	Representation string
	Function       string
}

// flowOccupancyAllowances names the representation adapters and the few
// lifecycle-only reads that do not answer caller-side Flow occupancy. Keeping
// this function-scoped makes a moved read fail instead of inheriting a broad
// file exemption. TestFlowOccupancySeam rejects entries after their read goes
// away, so this cannot become a permanent exception list.
var flowOccupancyAllowances = map[flowOccupancyAllowanceKey]string{
	{flowOccupancyLaunchOwners, "Model.flowLaunchAttempt"}:                              "turn the module-owned launch record into the lifecycle payload used for token and state transitions",
	{flowOccupancySessionOwners, "Model.admitSavedSessionFlowLaunch"}:                   "reject a duplicate saved-session reservation before the atomic reserve call reports the same conflict",
	{flowOccupancySessionOwners, "Model.flowLaunchSessionOwner"}:                        "expose the token-fenced owner identity to lifecycle tests and release bookkeeping",
	{flowOccupancyTerminalSlots, "Model.hasFlowEmbeddedTerminalForFlow"}:                "adapt retained terminal slots to flowOccupancyRuntime.HasFlowTerminal",
	{flowOccupancyTerminalSlots, "Model.hasFlowRepairEmbeddedTerminalForFlow"}:          "adapt repair terminal slots to flowOccupancyRuntime.HasRepairTerminal",
	{flowOccupancyTerminalSlots, "flowOccupancyRuntime.HasNonRepairFlowTerminal"}:       "adapt non-repair retained terminal slots to the occupancy Runtime interface",
	{flowOccupancyLease, "flowOccupancyLeaseInspector.FlowLeaseOccupied"}:               "adapt the injected or production lease inspector to the occupancy LeaseInspector interface",
	{flowOccupancyFlowSource, "flowOccupancyAuthoritativeFlow.ReadFlow"}:                "adapt the already-refreshed Flow record to the occupancy FlowReader interface",
	{flowOccupancySessionSource, "flowOccupancyAuthoritativeSessions.ListFlowSessions"}: "adapt the Flow-filtered session seam to the occupancy SessionStore interface",
}

type flowOccupancyViolation struct {
	File           string
	Function       string
	Representation string
	Line           int
}

func (violation flowOccupancyViolation) String() string {
	return fmt.Sprintf("%s:%d: %s reads %s", violation.File, violation.Line, violation.Function, violation.Representation)
}

type flowOccupancyExprKind uint8

const (
	flowOccupancyUnknown flowOccupancyExprKind = iota
	flowOccupancyOwnership
	flowOccupancySlot
	flowOccupancyLeaseAdapter
	flowOccupancyFlowAdapter
	flowOccupancySessionsAdapter
	flowOccupancyModel
)

type flowOccupancyFileScope struct {
	flowownershipPackages map[string]bool
}

func newFlowOccupancyFileScope(file *ast.File) flowOccupancyFileScope {
	scope := flowOccupancyFileScope{flowownershipPackages: map[string]bool{"flowownership": true}}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "github.com/approachcontrol/approach/flowownership" {
			continue
		}
		name := "flowownership"
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name != "." && name != "_" {
			scope.flowownershipPackages[name] = true
		}
	}
	return scope
}

func flowOccupancyTypeKind(expr ast.Expr, scope flowOccupancyFileScope) flowOccupancyExprKind {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowOccupancyTypeKind(expr.X, scope)
	case *ast.StarExpr:
		return flowOccupancyTypeKind(expr.X, scope)
	case *ast.IndexExpr:
		return flowOccupancyTypeKind(expr.X, scope)
	case *ast.IndexListExpr:
		return flowOccupancyTypeKind(expr.X, scope)
	case *ast.Ident:
		switch expr.Name {
		case "flowOccupancyLeaseInspector":
			return flowOccupancyLeaseAdapter
		case "flowOccupancyAuthoritativeFlow":
			return flowOccupancyFlowAdapter
		case "flowOccupancyAuthoritativeSessions":
			return flowOccupancySessionsAdapter
		case "Model":
			return flowOccupancyModel
		}
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		if !ok || !scope.flowownershipPackages[pkg.Name] {
			return flowOccupancyUnknown
		}
		switch expr.Sel.Name {
		case "Ownership":
			return flowOccupancyOwnership
		case "Slot":
			return flowOccupancySlot
		}
	}
	return flowOccupancyUnknown
}

func flowOccupancyExprType(expr ast.Expr, vars map[string]flowOccupancyExprKind, scope flowOccupancyFileScope) flowOccupancyExprKind {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowOccupancyExprType(expr.X, vars, scope)
	case *ast.UnaryExpr:
		return flowOccupancyExprType(expr.X, vars, scope)
	case *ast.Ident:
		return vars[expr.Name]
	case *ast.CompositeLit:
		return flowOccupancyTypeKind(expr.Type, scope)
	case *ast.CallExpr:
		if ident, ok := expr.Fun.(*ast.Ident); ok && ident.Name == "flowOwnershipSlot" {
			return flowOccupancySlot
		}
		return flowOccupancyTypeKind(expr.Fun, scope)
	case *ast.SelectorExpr:
		if expr.Sel.Name == "flowOwnership" && flowOccupancyExprType(expr.X, vars, scope) == flowOccupancyModel {
			return flowOccupancyOwnership
		}
	}
	return flowOccupancyUnknown
}

func flowOccupancyReceiverName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowOccupancyReceiverName(expr.X)
	case *ast.StarExpr:
		return flowOccupancyReceiverName(expr.X)
	case *ast.IndexExpr:
		return flowOccupancyReceiverName(expr.X)
	case *ast.IndexListExpr:
		return flowOccupancyReceiverName(expr.X)
	case *ast.Ident:
		return expr.Name
	case *ast.SelectorExpr:
		return expr.Sel.Name
	}
	return ""
}

func flowOccupancyFunctionName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	receiver := flowOccupancyReceiverName(function.Recv.List[0].Type)
	if receiver == "" {
		return function.Name.Name
	}
	return receiver + "." + function.Name.Name
}

func flowOccupancyDeclareFields(fields *ast.FieldList, vars map[string]flowOccupancyExprKind, scope flowOccupancyFileScope) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		kind := flowOccupancyTypeKind(field.Type, scope)
		for _, name := range field.Names {
			vars[name.Name] = kind
		}
	}
}

func flowOccupancyRepresentation(call *ast.CallExpr, vars map[string]flowOccupancyExprKind, scope flowOccupancyFileScope) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	kind := flowOccupancyExprType(selector.X, vars, scope)
	switch kind {
	case flowOccupancyOwnership:
		switch selector.Sel.Name {
		case "Occupied", "Lookup":
			return flowOccupancyLaunchOwners
		case "SessionOccupied", "SessionOwner":
			return flowOccupancySessionOwners
		}
	case flowOccupancySlot:
		switch selector.Sel.Name {
		case "HoldsFlow", "HoldsNonRepairFlow", "HoldsRepair":
			return flowOccupancyTerminalSlots
		}
	case flowOccupancyLeaseAdapter:
		if selector.Sel.Name == "inspect" {
			return flowOccupancyLease
		}
	case flowOccupancySessionsAdapter:
		if selector.Sel.Name == "list" {
			return flowOccupancySessionSource
		}
	}
	return ""
}

func scanFlowOccupancySource(name string, source []byte, allowances map[flowOccupancyAllowanceKey]string) ([]flowOccupancyViolation, error) {
	if strings.HasSuffix(name, "_test.go") {
		return nil, nil
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, name, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	scope := newFlowOccupancyFileScope(file)
	var violations []flowOccupancyViolation
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		functionName := flowOccupancyFunctionName(function)
		vars := make(map[string]flowOccupancyExprKind)
		if function.Recv != nil {
			flowOccupancyDeclareFields(function.Recv, vars, scope)
		}
		flowOccupancyDeclareFields(function.Type.Params, vars, scope)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.DeclStmt:
				declaration, ok := node.Decl.(*ast.GenDecl)
				if !ok {
					break
				}
				for _, raw := range declaration.Specs {
					spec, ok := raw.(*ast.ValueSpec)
					if !ok || spec.Type == nil {
						continue
					}
					kind := flowOccupancyTypeKind(spec.Type, scope)
					for _, ident := range spec.Names {
						vars[ident.Name] = kind
					}
				}
			case *ast.AssignStmt:
				for index, left := range node.Lhs {
					if index >= len(node.Rhs) {
						continue
					}
					ident, ok := left.(*ast.Ident)
					if ok {
						if kind := flowOccupancyExprType(node.Rhs[index], vars, scope); kind != flowOccupancyUnknown {
							vars[ident.Name] = kind
						}
					}
				}
			case *ast.CallExpr:
				representation := flowOccupancyRepresentation(node, vars, scope)
				if representation == "" {
					break
				}
				key := flowOccupancyAllowanceKey{representation, functionName}
				if _, allowed := allowances[key]; allowed {
					break
				}
				violations = append(violations, flowOccupancyViolation{
					File: name, Function: functionName, Representation: representation, Line: files.Position(node.Pos()).Line,
				})
			case *ast.SelectorExpr:
				if node.Sel.Name != "record" || flowOccupancyExprType(node.X, vars, scope) != flowOccupancyFlowAdapter {
					break
				}
				key := flowOccupancyAllowanceKey{flowOccupancyFlowSource, functionName}
				if _, allowed := allowances[key]; !allowed {
					violations = append(violations, flowOccupancyViolation{
						File: name, Function: functionName, Representation: flowOccupancyFlowSource, Line: files.Position(node.Pos()).Line,
					})
				}
			}
			return true
		})
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})
	return violations, nil
}

func TestFlowOccupancySeamMutationFixtures(t *testing.T) {
	tests := []struct {
		name           string
		representation string
		source         string
	}{
		{
			name:           "launch ownership",
			representation: "launch ownership map",
			source: `package model
import own "github.com/approachcontrol/approach/flowownership"
func forbidden(ownership own.Ownership[int, string]) bool { return ownership.Occupied("flow") }
`,
		},
		{
			name:           "saved session ownership",
			representation: "saved-session owner index",
			source: `package model
func forbidden(m Model, key flowLaunchSavedSessionKey) bool { return m.flowOwnership.SessionOccupied(key) }
`,
		},
		{
			name:           "retained terminal slot",
			representation: "retained embedded terminal slots",
			source: `package model
func forbidden(slot embeddedTerminalSlot) bool { return flowOwnershipSlot(slot).HoldsFlow("flow") }
`,
		},
		{
			name:           "Flow lease",
			representation: "Flow lease inspector",
			source: `package model
func forbidden(adapter flowOccupancyLeaseInspector) { _, _ = adapter.inspect("root", "flow") }
`,
		},
		{
			name:           "authoritative Flow",
			representation: "authoritative Flow read",
			source: `package model
func forbidden(source flowOccupancyAuthoritativeFlow) flowstore.FlowRecord { return source.record }
`,
		},
		{
			name:           "authoritative sessions",
			representation: "authoritative session reads",
			source: `package model
func forbidden(source flowOccupancyAuthoritativeSessions) { _, _ = source.list("flow") }
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			violations, err := scanFlowOccupancySource("fixture.go", []byte(test.source), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != 1 {
				t.Fatalf("got %d violations, want 1: %v", len(violations), violations)
			}
			diagnostic := violations[0].String()
			for _, want := range []string{"fixture.go", "forbidden", test.representation} {
				if !strings.Contains(diagnostic, want) {
					t.Fatalf("diagnostic %q does not contain %q", diagnostic, want)
				}
			}

			boundarySource := strings.Replace(test.source, "forbidden", "boundary", 1)
			allowances := map[flowOccupancyAllowanceKey]string{
				{test.representation, "boundary"}: "fixture boundary",
			}
			violations, err = scanFlowOccupancySource("fixture.go", []byte(boundarySource), allowances)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != 0 {
				t.Fatalf("boundary caller was rejected: %v", violations)
			}
		})
	}
}

func TestFlowOccupancySeamScannerPrecision(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		source string
	}{
		{
			name: "unrelated same-named type",
			file: "fixture.go",
			source: `package model
type Ownership struct{}
func (Ownership) Occupied(string) bool { return false }
func caller(value Ownership) bool { return value.Occupied("flow") }
`,
		},
		{
			name: "comment",
			file: "fixture.go",
			source: `package model
func caller() { /* m.flowOwnership.SessionOccupied(key) */ }
`,
		},
		{
			name: "test file",
			file: "fixture_test.go",
			source: `package model
func caller(m Model, key flowLaunchSavedSessionKey) bool { return m.flowOwnership.SessionOccupied(key) }
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			violations, err := scanFlowOccupancySource(test.file, []byte(test.source), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != 0 {
				t.Fatalf("got false positive: %v", violations)
			}
		})
	}
}

func flowOccupancyUnusedAllowances(allowances map[flowOccupancyAllowanceKey]string, hits []flowOccupancyViolation) []flowOccupancyAllowanceKey {
	used := make(map[flowOccupancyAllowanceKey]bool)
	for _, hit := range hits {
		used[flowOccupancyAllowanceKey{hit.Representation, hit.Function}] = true
	}
	var unused []flowOccupancyAllowanceKey
	for key := range allowances {
		if !used[key] {
			unused = append(unused, key)
		}
	}
	sort.Slice(unused, func(i, j int) bool {
		if unused[i].Representation != unused[j].Representation {
			return unused[i].Representation < unused[j].Representation
		}
		return unused[i].Function < unused[j].Function
	})
	return unused
}

func TestFlowOccupancySeamRejectsStaleAllowance(t *testing.T) {
	hits := []flowOccupancyViolation{{Representation: flowOccupancyLease, Function: "boundary"}}
	allowances := map[flowOccupancyAllowanceKey]string{
		{flowOccupancyLease, "boundary"}: "used",
		{flowOccupancyLease, "removed"}:  "stale",
	}
	unused := flowOccupancyUnusedAllowances(allowances, hits)
	if len(unused) != 1 || unused[0].Function != "removed" {
		t.Fatalf("unused allowances = %v, want removed", unused)
	}
}

func TestFlowOccupancySeam(t *testing.T) {
	root := ".."
	var hits []flowOccupancyViolation
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if rel == "flowownership" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found, err := scanFlowOccupancySource(filepath.ToSlash(rel), source, nil)
		if err != nil {
			return err
		}
		hits = append(hits, found...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for key, reason := range flowOccupancyAllowances {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("allowance for %s in %s has no reason", key.Representation, key.Function)
		}
	}
	for _, hit := range hits {
		key := flowOccupancyAllowanceKey{hit.Representation, hit.Function}
		if _, ok := flowOccupancyAllowances[key]; !ok {
			t.Errorf("Flow occupancy seam violation: %s", hit)
		}
	}
	for _, key := range flowOccupancyUnusedAllowances(flowOccupancyAllowances, hits) {
		t.Errorf("stale Flow occupancy allowance: %s in %s", key.Representation, key.Function)
	}
}

// validateFlowLaunchOccupancy is intentionally a separate store invariant.
// It validates a phase transition against other running phases while the store
// owns the record. It is not a caller-side occupancy query and must stay in
// flowstore rather than moving into flowownership.
func TestFlowStoreLaunchOccupancyValidationStaysStoreSide(t *testing.T) {
	path := filepath.Join("..", "flowstore", "store.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "validateFlowLaunchOccupancy" {
			return
		}
	}
	t.Fatal("flowstore.validateFlowLaunchOccupancy is missing; keep the store-side phase-transition invariant separate from caller-side occupancy")
}
