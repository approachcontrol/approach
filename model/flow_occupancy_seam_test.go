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
	flowOccupancyLaunchOwners       = "launch ownership map"
	flowOccupancySessionOwners      = "saved-session owner index"
	flowOccupancyTerminalSlots      = "retained embedded terminal slots"
	flowOccupancyLease              = "Flow lease inspector"
	flowOccupancyFlowSource         = "authoritative Flow read"
	flowOccupancySessionSource      = "authoritative session reads"
	flowOccupancyFlowCacheSource    = "cached Flow read"
	flowOccupancySessionCacheSource = "cached session reads"
	flowOccupancyRuntimeState       = "model-side occupancy runtime"
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
	flowOccupancyKnownLeaseAdapter
	flowOccupancyFlowCacheAdapter
	flowOccupancySessionCacheAdapter
	flowOccupancyRuntimeAdapter
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
		case "flowOccupancyKnownLease":
			return flowOccupancyKnownLeaseAdapter
		case "flowOccupancyFlowCache":
			return flowOccupancyFlowCacheAdapter
		case "flowOccupancySessionCache":
			return flowOccupancySessionCacheAdapter
		case "flowOccupancyRuntime":
			return flowOccupancyRuntimeAdapter
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

func flowOccupancyExprType(expr ast.Expr, assignments map[*ast.Object]flowOccupancyExprKind, scope flowOccupancyFileScope) flowOccupancyExprKind {
	return flowOccupancyExprTypeSeen(expr, assignments, scope, make(map[*ast.Object]bool))
}

func flowOccupancyExprTypeSeen(expr ast.Expr, assignments map[*ast.Object]flowOccupancyExprKind, scope flowOccupancyFileScope, seen map[*ast.Object]bool) flowOccupancyExprKind {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowOccupancyExprTypeSeen(expr.X, assignments, scope, seen)
	case *ast.UnaryExpr:
		return flowOccupancyExprTypeSeen(expr.X, assignments, scope, seen)
	case *ast.Ident:
		return flowOccupancyObjectType(expr.Obj, assignments, scope, seen)
	case *ast.CompositeLit:
		return flowOccupancyTypeKind(expr.Type, scope)
	case *ast.CallExpr:
		if ident, ok := expr.Fun.(*ast.Ident); ok && ident.Name == "flowOwnershipSlot" {
			return flowOccupancySlot
		}
		return flowOccupancyTypeKind(expr.Fun, scope)
	case *ast.SelectorExpr:
		if expr.Sel.Name == "flowOwnership" && flowOccupancyExprTypeSeen(expr.X, assignments, scope, seen) == flowOccupancyModel {
			return flowOccupancyOwnership
		}
	}
	return flowOccupancyUnknown
}

func flowOccupancyObjectType(object *ast.Object, assignments map[*ast.Object]flowOccupancyExprKind, scope flowOccupancyFileScope, seen map[*ast.Object]bool) flowOccupancyExprKind {
	if object == nil || seen[object] {
		return flowOccupancyUnknown
	}
	if kind, assigned := assignments[object]; assigned {
		return kind
	}
	seen[object] = true
	defer delete(seen, object)

	switch declaration := object.Decl.(type) {
	case *ast.Field:
		return flowOccupancyTypeKind(declaration.Type, scope)
	case *ast.ValueSpec:
		if declaration.Type != nil {
			return flowOccupancyTypeKind(declaration.Type, scope)
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

func flowOccupancyRepresentation(call *ast.CallExpr, assignments map[*ast.Object]flowOccupancyExprKind, scope flowOccupancyFileScope) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	kind := flowOccupancyExprType(selector.X, assignments, scope)
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
		if selector.Sel.Name == "inspect" || selector.Sel.Name == "FlowLeaseOccupied" {
			return flowOccupancyLease
		}
	case flowOccupancyKnownLeaseAdapter:
		if selector.Sel.Name == "FlowLeaseOccupied" {
			return flowOccupancyLease
		}
	case flowOccupancyFlowAdapter:
		if selector.Sel.Name == "ReadFlow" {
			return flowOccupancyFlowSource
		}
	case flowOccupancySessionsAdapter:
		if selector.Sel.Name == "list" || selector.Sel.Name == "ListFlowSessions" {
			return flowOccupancySessionSource
		}
	case flowOccupancyFlowCacheAdapter:
		if selector.Sel.Name == "CachedFlow" {
			return flowOccupancyFlowCacheSource
		}
	case flowOccupancySessionCacheAdapter:
		if selector.Sel.Name == "ActiveFlowSessions" {
			return flowOccupancySessionCacheSource
		}
	case flowOccupancyRuntimeAdapter:
		switch selector.Sel.Name {
		case "AttemptHolder":
			return flowOccupancyLaunchOwners
		case "HasFlowTerminal", "HasNonRepairFlowTerminal", "HasRepairTerminal":
			return flowOccupancyTerminalSlots
		case "HeadlessWritePending", "RepairDrainPending":
			return flowOccupancyRuntimeState
		}
	case flowOccupancyModel:
		switch selector.Sel.Name {
		case "flowLaunchAttemptOccupied":
			return flowOccupancyLaunchOwners
		case "hasFlowEmbeddedTerminalForFlow", "hasFlowRepairEmbeddedTerminalForFlow":
			return flowOccupancyTerminalSlots
		case "flowHeadlessWritePending", "hasPendingRepairAutoDrainMarker":
			return flowOccupancyRuntimeState
		}
	}
	return ""
}

type flowOccupancyScanEvent struct {
	position    token.Pos
	assignment  *ast.AssignStmt
	call        *ast.CallExpr
	declaration *ast.ValueSpec
	selector    *ast.SelectorExpr
}

func scanFlowOccupancySource(name string, source []byte, allowances map[flowOccupancyAllowanceKey]string) ([]flowOccupancyViolation, error) {
	if strings.HasSuffix(name, "_test.go") {
		return nil, nil
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, name, source, 0)
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
		assignments := make(map[*ast.Object]flowOccupancyExprKind)
		var events []flowOccupancyScanEvent
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.AssignStmt:
				events = append(events, flowOccupancyScanEvent{position: node.End(), assignment: node})
			case *ast.CallExpr:
				events = append(events, flowOccupancyScanEvent{position: node.Pos(), call: node})
			case *ast.ValueSpec:
				events = append(events, flowOccupancyScanEvent{position: node.End(), declaration: node})
			case *ast.SelectorExpr:
				if node.Sel.Name == "record" {
					events = append(events, flowOccupancyScanEvent{position: node.Pos(), selector: node})
				}
			}
			return true
		})
		sort.SliceStable(events, func(i, j int) bool { return events[i].position < events[j].position })
		for _, event := range events {
			switch {
			case event.declaration != nil:
				for index, name := range event.declaration.Names {
					if name.Obj == nil || index >= len(event.declaration.Values) {
						continue
					}
					kind := flowOccupancyExprType(event.declaration.Values[index], assignments, scope)
					if kind != flowOccupancyUnknown {
						assignments[name.Obj] = kind
					}
				}
			case event.assignment != nil:
				type update struct {
					object *ast.Object
					kind   flowOccupancyExprKind
				}
				var updates []update
				for index, left := range event.assignment.Lhs {
					if index >= len(event.assignment.Rhs) {
						continue
					}
					name, ok := left.(*ast.Ident)
					if !ok || name.Obj == nil {
						continue
					}
					updates = append(updates, update{
						object: name.Obj,
						kind:   flowOccupancyExprType(event.assignment.Rhs[index], assignments, scope),
					})
				}
				for _, update := range updates {
					if update.kind != flowOccupancyUnknown {
						assignments[update.object] = update.kind
					}
				}
			case event.call != nil:
				representation := flowOccupancyRepresentation(event.call, assignments, scope)
				if representation == "" {
					continue
				}
				key := flowOccupancyAllowanceKey{representation, functionName}
				if _, allowed := allowances[key]; allowed {
					continue
				}
				violations = append(violations, flowOccupancyViolation{
					File: name, Function: functionName, Representation: representation, Line: files.Position(event.call.Pos()).Line,
				})
			case event.selector != nil:
				if flowOccupancyExprType(event.selector.X, assignments, scope) != flowOccupancyFlowAdapter {
					continue
				}
				key := flowOccupancyAllowanceKey{flowOccupancyFlowSource, functionName}
				if _, allowed := allowances[key]; !allowed {
					violations = append(violations, flowOccupancyViolation{
						File: name, Function: functionName, Representation: flowOccupancyFlowSource, Line: files.Position(event.selector.Pos()).Line,
					})
				}
			}
		}
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
			name:           "inferred launch ownership variable",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model) bool {
	var ownership = m.flowOwnership
	return ownership.Occupied("flow")
}
`,
		},
		{
			name:           "assigned launch ownership variable",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model) bool {
	var ownership interface { Occupied(string) bool }
	ownership = m.flowOwnership
	return ownership.Occupied("flow")
}
`,
		},
		{
			name:           "explicit interface launch ownership initializer",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model) bool {
	var ownership interface { Occupied(string) bool } = m.flowOwnership
	return ownership.Occupied("flow")
}
`,
		},
		{
			name:           "simultaneous assignment reads prior launch ownership",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model) bool {
	var occupied bool
	ownership := m.flowOwnership
	ownership, occupied = nil, ownership.Occupied("flow")
	return occupied
}
`,
		},
		{
			name:           "unresolved reassignment retains possible launch ownership",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model) bool {
	ownership := m.flowOwnership
	ownership = getOwnership()
	return ownership.Occupied("flow")
}
`,
		},
		{
			name:           "conditional reassignment retains possible launch ownership",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model, condition bool) bool {
	ownership := m.flowOwnership
	if condition {
		ownership = unrelatedOwnership{}
	}
	return ownership.Occupied("flow")
}
`,
		},
		{
			name:           "initializer alias retains copied launch ownership",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model) bool {
	ownership := m.flowOwnership
	var alias = ownership
	ownership = unrelatedOwnership{}
	return alias.Occupied("flow")
}
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
			name:           "Flow lease adapter method",
			representation: "Flow lease inspector",
			source: `package model
func forbidden(adapter flowOccupancyLeaseInspector) { _, _ = adapter.FlowLeaseOccupied("flow") }
`,
		},
		{
			name:           "known Flow lease adapter method",
			representation: "Flow lease inspector",
			source: `package model
func forbidden(adapter flowOccupancyKnownLease) { _, _ = adapter.FlowLeaseOccupied("flow") }
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
			name:           "authoritative Flow adapter method",
			representation: "authoritative Flow read",
			source: `package model
func forbidden(source flowOccupancyAuthoritativeFlow) { _, _ = source.ReadFlow("flow") }
`,
		},
		{
			name:           "authoritative sessions",
			representation: "authoritative session reads",
			source: `package model
func forbidden(source flowOccupancyAuthoritativeSessions) { _, _ = source.list("flow") }
`,
		},
		{
			name:           "authoritative sessions adapter method",
			representation: "authoritative session reads",
			source: `package model
func forbidden(source flowOccupancyAuthoritativeSessions) { _, _ = source.ListFlowSessions("flow") }
`,
		},
		{
			name:           "cached Flow adapter method",
			representation: "cached Flow read",
			source: `package model
func forbidden(cache flowOccupancyFlowCache) { _, _ = cache.CachedFlow("flow") }
`,
		},
		{
			name:           "cached sessions adapter method",
			representation: "cached session reads",
			source: `package model
func forbidden(cache flowOccupancySessionCache) { _ = cache.ActiveFlowSessions("flow") }
`,
		},
		{
			name:           "runtime attempt holder",
			representation: "launch ownership map",
			source: `package model
func forbidden(runtime flowOccupancyRuntime) { _, _ = runtime.AttemptHolder("flow") }
`,
		},
		{
			name:           "runtime Flow terminal",
			representation: "retained embedded terminal slots",
			source: `package model
func forbidden(runtime flowOccupancyRuntime) bool { return runtime.HasFlowTerminal("flow") }
`,
		},
		{
			name:           "runtime non-repair Flow terminal",
			representation: "retained embedded terminal slots",
			source: `package model
func forbidden(runtime flowOccupancyRuntime) bool { return runtime.HasNonRepairFlowTerminal("flow") }
`,
		},
		{
			name:           "runtime repair terminal",
			representation: "retained embedded terminal slots",
			source: `package model
func forbidden(runtime flowOccupancyRuntime) bool { return runtime.HasRepairTerminal("flow") }
`,
		},
		{
			name:           "runtime pending headless write",
			representation: "model-side occupancy runtime",
			source: `package model
func forbidden(runtime flowOccupancyRuntime) bool { return runtime.HeadlessWritePending("flow") }
`,
		},
		{
			name:           "runtime pending repair drain",
			representation: "model-side occupancy runtime",
			source: `package model
func forbidden(runtime flowOccupancyRuntime) bool { return runtime.RepairDrainPending("flow") }
`,
		},
		{
			name:           "Model launch occupancy wrapper",
			representation: "launch ownership map",
			source: `package model
func forbidden(m Model) bool { return m.flowLaunchAttemptOccupied("flow") }
`,
		},
		{
			name:           "Model terminal occupancy wrapper",
			representation: "retained embedded terminal slots",
			source: `package model
func forbidden(m Model) bool { return m.hasFlowEmbeddedTerminalForFlow("flow") }
`,
		},
		{
			name:           "Model repair terminal occupancy wrapper",
			representation: "retained embedded terminal slots",
			source: `package model
func forbidden(m Model) bool { return m.hasFlowRepairEmbeddedTerminalForFlow("flow") }
`,
		},
		{
			name:           "Model headless occupancy wrapper",
			representation: "model-side occupancy runtime",
			source: `package model
func forbidden(m Model) bool { return m.flowHeadlessWritePending("flow") }
`,
		},
		{
			name:           "Model repair drain occupancy wrapper",
			representation: "model-side occupancy runtime",
			source: `package model
func forbidden(m Model) bool { return m.hasPendingRepairAutoDrainMarker("flow") }
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
			name: "unrelated value shadows protected parameter",
			file: "fixture.go",
			source: `package model
import own "github.com/approachcontrol/approach/flowownership"
type unrelatedOwnership struct{}
func (unrelatedOwnership) Occupied(string) bool { return false }
func caller(ownership own.Ownership[int, string]) bool {
	if true {
		ownership := unrelatedOwnership{}
		return ownership.Occupied("flow")
	}
	return false
}
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
