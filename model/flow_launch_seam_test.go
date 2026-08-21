package model

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/actions"
)

// The Flow launch seam is a deep module: every Flow launch context is built by
// the role builder in model/flow_launch_context.go, so the eleven Flow marker
// fields on actions.AgentLaunchContext are set in exactly one place. That is a
// property of the repository's shape, not of any single call, so it is checked
// by scanning source rather than by exercising behavior.
//
// The scan follows a context through aliases, struct fields of a known owner,
// helper results, and slice and map elements, but it reads syntax rather than
// types, so flowLaunchContextRequiresLifecycle stays the runtime backstop for
// whatever it still cannot see: a context reached through an interface value,
// through a function value, or through a base whose type the file never
// spells. This test is the earlier, louder half of the same guard.

// flowSeamLaunchContextExemptFile is the launch module itself — the one file
// allowed to construct Flow-marked launch contexts.
const flowSeamLaunchContextExemptFile = "flow_launch_context.go"

// flowSeamViolation is one marker field set on a launch context outside the
// launch module. Func is the enclosing top-level function, receiver-qualified,
// or empty for a package-level declaration.
type flowSeamViolation struct {
	Dir   string
	File  string
	Func  string
	Field string
}

func (v flowSeamViolation) String() string {
	where := v.Func
	if where == "" {
		where = "(package level)"
	}
	return fmt.Sprintf("%s/%s: %s sets %s", v.Dir, v.File, where, v.Field)
}

// flowSeamActionsImportPath is the package the launch context type lives in.
const flowSeamActionsImportPath = "github.com/approachcontrol/approach/actions"

// flowSeamActionsNames is every identifier that names the actions package in
// file: whatever its import declares it as, since `import act ".../actions"`
// spells the same type `act.AgentLaunchContext`. The unaliased name is always
// included so a source fragment without imports still reads normally.
func flowSeamActionsNames(file *ast.File) map[string]bool {
	names := map[string]bool{"actions": true}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != flowSeamActionsImportPath {
			continue
		}
		if imported.Name != nil && imported.Name.Name != "_" && imported.Name.Name != "." {
			names[imported.Name.Name] = true
		}
	}
	return names
}

// flowSeamIsLaunchContextType matches the two syntactic spellings of the type:
// a qualified actions.AgentLaunchContext under whatever name the file imports
// the actions package as, and the bare ident from inside actions itself.
func flowSeamIsLaunchContextType(expr ast.Expr, pkgs map[string]bool) bool {
	switch typ := expr.(type) {
	case *ast.Ident:
		return typ.Name == "AgentLaunchContext"
	case *ast.StarExpr:
		return flowSeamIsLaunchContextType(typ.X, pkgs)
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		return ok && pkgs[pkg.Name] && typ.Sel.Name == "AgentLaunchContext"
	}
	return false
}

// flowSeamLaunchContextLit unwraps the expressions that still yield a launch
// context: parentheses, an address-of, and applyLaunchStamp, which the builder
// roles wrap their literals in. Anything else returns nil, because an
// assignment target has to be *provably* a launch context before a marker
// write on it counts as a violation.
func flowSeamLaunchContextLit(expr ast.Expr, pkgs map[string]bool) *ast.CompositeLit {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamLaunchContextLit(expr.X, pkgs)
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return flowSeamLaunchContextLit(expr.X, pkgs)
		}
	case *ast.CallExpr:
		if fn, ok := expr.Fun.(*ast.Ident); ok && fn.Name == "applyLaunchStamp" && len(expr.Args) > 0 {
			return flowSeamLaunchContextLit(expr.Args[0], pkgs)
		}
	case *ast.CompositeLit:
		if flowSeamIsLaunchContextType(expr.Type, pkgs) {
			return expr
		}
	}
	return nil
}

// flowSeamKind is what an expression was found to yield: a launch context, a
// slice or map of them, and/or a value of a named type declared in this
// repository. The named type is what lets a field read be judged by its owner
// rather than by field name alone.
type flowSeamKind struct {
	context    bool
	collection bool
	named      string
}

func (k flowSeamKind) known() bool {
	return k.context || k.collection || k.named != ""
}

// flowSeamTypeKind classifies a type expression: an AgentLaunchContext, a
// slice/array/map/variadic of them, or a named type of this package.
func flowSeamTypeKind(expr ast.Expr, pkgs map[string]bool) flowSeamKind {
	switch typ := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamTypeKind(typ.X, pkgs)
	case *ast.StarExpr:
		return flowSeamTypeKind(typ.X, pkgs)
	case *ast.Ellipsis:
		if flowSeamIsLaunchContextType(typ.Elt, pkgs) {
			return flowSeamKind{collection: true}
		}
	case *ast.ArrayType:
		if flowSeamIsLaunchContextType(typ.Elt, pkgs) {
			return flowSeamKind{collection: true}
		}
	case *ast.MapType:
		if flowSeamIsLaunchContextType(typ.Value, pkgs) {
			return flowSeamKind{collection: true}
		}
	case *ast.Ident:
		if typ.Name == "AgentLaunchContext" {
			return flowSeamKind{context: true}
		}
		return flowSeamKind{named: typ.Name}
	case *ast.SelectorExpr:
		if flowSeamIsLaunchContextType(typ, pkgs) {
			return flowSeamKind{context: true}
		}
	}
	return flowSeamKind{}
}

// flowSeamContextShapes is what the scan set has to be read for before any
// marker write in it can be judged: which field of which struct holds a launch
// context, and which functions hand one back. Both are declared in one file
// and used in another, so they are merged across every scanned file first.
type flowSeamContextShapes struct {
	// fields is owner type name -> field name -> what that field holds, so a
	// write through one — `msg.LaunchContext.FlowRepair = true` — is judged by
	// the struct it belongs to. Keying by owner is what keeps an unrelated
	// struct that happens to declare a `LaunchContext` field of some other
	// type from failing the guard.
	fields map[string]map[string]flowSeamKind
	// results maps a function name to the indices of its results that are
	// launch contexts, so `ctx := makeContext(); ctx.FlowRepair = true` is
	// caught the same way a literal binding is. Receivers are ignored: two
	// functions sharing a name is rare, and conflating them only makes the
	// seam rule stricter.
	results map[string]map[int]bool
}

// flowSeamScope pairs the scan-set-wide shapes with the one thing that is
// per-file: which identifiers name the actions package here.
type flowSeamScope struct {
	pkgs   map[string]bool
	shapes flowSeamContextShapes
}

func newFlowSeamContextShapes() flowSeamContextShapes {
	return flowSeamContextShapes{
		fields:  make(map[string]map[string]flowSeamKind),
		results: make(map[string]map[int]bool),
	}
}

func (s flowSeamContextShapes) merge(other flowSeamContextShapes) {
	for owner, fields := range other.fields {
		if s.fields[owner] == nil {
			s.fields[owner] = make(map[string]flowSeamKind)
		}
		for name, kind := range fields {
			s.fields[owner][name] = kind
		}
	}
	for name, indices := range other.results {
		if s.results[name] == nil {
			s.results[name] = make(map[int]bool)
		}
		for index := range indices {
			s.results[name][index] = true
		}
	}
}

func (s flowSeamContextShapes) contextFieldCount() int {
	count := 0
	for _, fields := range s.fields {
		for _, kind := range fields {
			if kind.context {
				count++
			}
		}
	}
	return count
}

// resultIndices reports which results of the called function are launch
// contexts, for a call whose callee is a plain name or a method selector.
func (s flowSeamContextShapes) resultIndices(call *ast.CallExpr) map[int]bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return s.results[fn.Name]
	case *ast.SelectorExpr:
		return s.results[fn.Sel.Name]
	}
	return nil
}

func flowSeamContextShapesOf(file *ast.File) flowSeamContextShapes {
	shapes := newFlowSeamContextShapes()
	pkgs := flowSeamActionsNames(file)
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.TypeSpec:
			structType, ok := n.Type.(*ast.StructType)
			if !ok || structType.Fields == nil {
				return true
			}
			for _, field := range structType.Fields.List {
				kind := flowSeamTypeKind(field.Type, pkgs)
				if !kind.known() {
					continue
				}
				for _, name := range field.Names {
					if shapes.fields[n.Name.Name] == nil {
						shapes.fields[n.Name.Name] = make(map[string]flowSeamKind)
					}
					shapes.fields[n.Name.Name][name.Name] = kind
				}
			}
		case *ast.FuncDecl:
			if n.Type == nil || n.Type.Results == nil {
				return true
			}
			indices := make(map[int]bool)
			position := 0
			for _, result := range n.Type.Results.List {
				count := len(result.Names)
				if count == 0 {
					count = 1
				}
				if flowSeamIsLaunchContextType(result.Type, pkgs) {
					for offset := 0; offset < count; offset++ {
						indices[position+offset] = true
					}
				}
				position += count
			}
			if len(indices) > 0 {
				shapes.results[n.Name.Name] = indices
			}
		}
		return true
	})
	return shapes
}

// flowSeamLocals is what one declaration's names were found to hold. It is one
// flat namespace rather than a stack of lexical scopes: a name that ever holds
// a launch context anywhere in the declaration keeps that classification, so a
// closure that shadows `ctx` with something else cannot hide the outer marker
// write. The cost is that the reverse shadowing reports a write the compiler
// would bind elsewhere — loud and rare, and the kind of thing a human reads
// once, where the silent miss is the failure that matters.
type flowSeamLocals struct {
	kinds   map[string]flowSeamKind
	changed bool
}

func newFlowSeamLocals() *flowSeamLocals {
	return &flowSeamLocals{kinds: make(map[string]flowSeamKind)}
}

func (l *flowSeamLocals) kind(name string) flowSeamKind {
	return l.kinds[name]
}

func (l *flowSeamLocals) learn(name string, kind flowSeamKind) {
	if name == "" || name == "_" || !kind.known() {
		return
	}
	// Learning only ever moves a name up the rank, never sideways or down.
	// That is what keeps a shadowing binding from trading a launch context
	// away, and what makes the repeated walk terminate.
	existing := l.kinds[name]
	if flowSeamKindRank(kind) <= flowSeamKindRank(existing) {
		return
	}
	l.kinds[name] = kind
	l.changed = true
}

// flowSeamKindRank orders what a name can be found to hold, most specific
// last: a launch context outranks a collection of them, which outranks a
// value of some named type.
func flowSeamKindRank(kind flowSeamKind) int {
	switch {
	case kind.context:
		return 3
	case kind.collection:
		return 2
	case kind.named != "":
		return 1
	}
	return 0
}

// flowSeamExprKind resolves what an expression yields. It follows the shapes a
// launch context actually travels through in this repository — names, struct
// fields of a known owner, helper results, slice and map elements — through
// parentheses, address-of, and dereference. It reads syntax, not types, so an
// expression it cannot place comes back unknown rather than guessed at.
func flowSeamExprKind(expr ast.Expr, locals *flowSeamLocals, scope flowSeamScope) flowSeamKind {
	if flowSeamLaunchContextLit(expr, scope.pkgs) != nil {
		return flowSeamKind{context: true}
	}
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamExprKind(expr.X, locals, scope)
	case *ast.StarExpr:
		return flowSeamExprKind(expr.X, locals, scope)
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return flowSeamExprKind(expr.X, locals, scope)
		}
	case *ast.CompositeLit:
		return flowSeamTypeKind(expr.Type, scope.pkgs)
	case *ast.Ident:
		return locals.kind(expr.Name)
	case *ast.SelectorExpr:
		owner := flowSeamExprKind(expr.X, locals, scope).named
		if owner == "" {
			return flowSeamKind{}
		}
		return scope.shapes.fields[owner][expr.Sel.Name]
	case *ast.IndexExpr:
		if flowSeamExprKind(expr.X, locals, scope).collection {
			return flowSeamKind{context: true}
		}
	case *ast.CallExpr:
		if scope.shapes.resultIndices(expr)[0] {
			return flowSeamKind{context: true}
		}
	}
	return flowSeamKind{}
}

// flowSeamLaunchContextNames collects what every name in node holds:
// parameters and results of the function and of any closure inside it, `var`
// declarations, and bindings of a literal, of another such name, of a struct
// field, of a helper result, or of a slice or map element. Bindings chain, so
// the walk repeats until it stops learning names.
func flowSeamLaunchContextNames(node ast.Node, scope flowSeamScope) *flowSeamLocals {
	locals := newFlowSeamLocals()
	for {
		locals.changed = false
		flowSeamCollectLaunchContextNames(node, locals, scope)
		if !locals.changed {
			return locals
		}
	}
}

// flowSeamBindCallResults records the names on the left of a single-call
// assignment whose matching result is a launch context, covering the
// multi-result `ctx, decision, err := build()` shape as well as the single one.
func flowSeamBindCallResults(lhs, rhs []ast.Expr, locals *flowSeamLocals, scope flowSeamScope) {
	if len(rhs) != 1 {
		return
	}
	call, ok := rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}
	indices := scope.shapes.resultIndices(call)
	for index, target := range lhs {
		if !indices[index] {
			continue
		}
		if ident, ok := target.(*ast.Ident); ok {
			locals.learn(ident.Name, flowSeamKind{context: true})
		}
	}
}

func flowSeamCollectLaunchContextNames(node ast.Node, locals *flowSeamLocals, scope flowSeamScope) {
	addFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			kind := flowSeamTypeKind(field.Type, scope.pkgs)
			for _, name := range field.Names {
				locals.learn(name.Name, kind)
			}
		}
	}
	addFuncType := func(typ *ast.FuncType) {
		if typ == nil {
			return
		}
		addFields(typ.Params)
		addFields(typ.Results)
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			addFuncType(n.Type)
			addFields(n.Recv)
		case *ast.FuncLit:
			addFuncType(n.Type)
		case *ast.ValueSpec:
			if n.Type != nil {
				kind := flowSeamTypeKind(n.Type, scope.pkgs)
				for _, name := range n.Names {
					locals.learn(name.Name, kind)
				}
			}
			for index, value := range n.Values {
				if index < len(n.Names) {
					locals.learn(n.Names[index].Name, flowSeamExprKind(value, locals, scope))
				}
			}
			specNames := make([]ast.Expr, 0, len(n.Names))
			for _, name := range n.Names {
				specNames = append(specNames, name)
			}
			flowSeamBindCallResults(specNames, n.Values, locals, scope)
		case *ast.AssignStmt:
			for index, value := range n.Rhs {
				if index >= len(n.Lhs) {
					continue
				}
				if ident, ok := n.Lhs[index].(*ast.Ident); ok {
					locals.learn(ident.Name, flowSeamExprKind(value, locals, scope))
				}
			}
			flowSeamBindCallResults(n.Lhs, n.Rhs, locals, scope)
		case *ast.RangeStmt:
			if flowSeamExprKind(n.X, locals, scope).collection {
				if ident, ok := n.Value.(*ast.Ident); ok {
					locals.learn(ident.Name, flowSeamKind{context: true})
				}
			}
		}
		return true
	})
}

// flowSeamLiteralMarkers records every marker field keyed in one launch
// context literal.
func flowSeamLiteralMarkers(lit *ast.CompositeLit, markers map[string]bool, record func(string)) {
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := pair.Key.(*ast.Ident); ok && markers[key.Name] {
			record(key.Name)
		}
	}
}

// flowSeamNodeViolations reports marker fields set within one declaration:
// keys on a launch context literal, including the elided element literals of a
// slice or map of contexts, and assignments to a marker field of any
// expression that provably yields a launch context.
func flowSeamNodeViolations(dir, file, function string, node ast.Node, markers map[string]bool, scope flowSeamScope) []flowSeamViolation {
	locals := flowSeamLaunchContextNames(node, scope)
	var violations []flowSeamViolation
	record := func(field string) {
		violations = append(violations, flowSeamViolation{Dir: dir, File: file, Func: function, Field: field})
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			if flowSeamIsLaunchContextType(n.Type, scope.pkgs) {
				flowSeamLiteralMarkers(n, markers, record)
				return true
			}
			if !flowSeamTypeKind(n.Type, scope.pkgs).collection {
				return true
			}
			// `[]actions.AgentLaunchContext{{FlowRepair: true}}`: the element
			// literals leave the type elided, so they are only recognizable
			// from the collection that holds them.
			for _, element := range n.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					element = pair.Value
				}
				if lit, ok := element.(*ast.CompositeLit); ok && lit.Type == nil {
					flowSeamLiteralMarkers(lit, markers, record)
				}
			}
		case *ast.AssignStmt:
			for _, target := range n.Lhs {
				selector, ok := target.(*ast.SelectorExpr)
				if !ok || !markers[selector.Sel.Name] {
					continue
				}
				if flowSeamExprKind(selector.X, locals, scope).context {
					record(selector.Sel.Name)
				}
			}
		}
		return true
	})
	return violations
}

// flowSeamViolations reports every marker field set on a launch context in one
// file, attributed to its enclosing top-level function. The launch module
// itself is exempt.
func flowSeamViolations(dir, name string, file *ast.File, markers map[string]bool, shapes flowSeamContextShapes) []flowSeamViolation {
	if dir == "model" && name == flowSeamLaunchContextExemptFile {
		return nil
	}
	scope := flowSeamScope{pkgs: flowSeamActionsNames(file), shapes: shapes}
	var violations []flowSeamViolation
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			violations = append(violations, flowSeamNodeViolations(dir, name, "", declaration, markers, scope)...)
			continue
		}
		if function.Body == nil {
			continue
		}
		label := function.Name.Name
		if function.Recv != nil && len(function.Recv.List) == 1 {
			if receiver := flowLaunchReceiverName(function.Recv.List[0].Type); receiver != "" {
				label = receiver + "." + label
			}
		}
		violations = append(violations, flowSeamNodeViolations(dir, name, label, function, markers, scope)...)
	}
	return violations
}

func flowSeamMarkerFields() map[string]bool {
	markers := make(map[string]bool)
	structType := reflect.TypeOf(actions.AgentLaunchContext{})
	for index := 0; index < structType.NumField(); index++ {
		if name := structType.Field(index).Name; strings.HasPrefix(name, "Flow") {
			markers[name] = true
		}
	}
	return markers
}

// TestFlowLaunchMarkerFieldsAreExactlyTheEleven pins the derived marker set
// against the fields ADR 0002 names. Adding a twelfth Flow* field extends the
// seam rule automatically; renaming one fails here rather than silently
// shrinking the rule.
func TestFlowLaunchMarkerFieldsAreExactlyTheEleven(t *testing.T) {
	expected := []string{
		"FlowAgent",
		"FlowAutoLaunch",
		"FlowAutofix",
		"FlowAutofixPRNumber",
		"FlowID",
		"FlowLaunchTracked",
		"FlowPhaseID",
		"FlowPhaseKind",
		"FlowPhaseTerminal",
		"FlowRepair",
		"FlowSavedSessionResume",
	}
	derived := make([]string, 0, len(expected))
	for name := range flowSeamMarkerFields() {
		derived = append(derived, name)
	}
	sort.Strings(derived)
	if strings.Join(derived, ",") != strings.Join(expected, ",") {
		t.Fatalf("Flow marker fields changed:\n got %v\nwant %v", derived, expected)
	}
}

// flowSeamViolationsForSource judges source the way the repository scan does:
// shapes merged from source and from every neighbor, then violations read off
// source alone. Neighbors matter because a field declared in one file is
// written through in another, and because a name shared with a real launch
// context field must not turn an unrelated struct into a violation.
func flowSeamViolationsForSource(t *testing.T, dir, name, source string, neighbors ...string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	shapes := newFlowSeamContextShapes()
	shapes.merge(flowSeamContextShapesOf(file))
	for index, neighbor := range neighbors {
		parsed, err := parser.ParseFile(fset, fmt.Sprintf("neighbor%d.go", index), neighbor, 0)
		if err != nil {
			t.Fatalf("parse neighbor %d: %v", index, err)
		}
		shapes.merge(flowSeamContextShapesOf(parsed))
	}
	found := flowSeamViolations(dir, name, file, flowSeamMarkerFields(), shapes)
	reported := make([]string, 0, len(found))
	for _, violation := range found {
		reported = append(reported, violation.Func+"."+violation.Field)
	}
	sort.Strings(reported)
	return reported
}

func TestFlowSeamDetectorFindsOutOfModuleMarkers(t *testing.T) {
	tests := map[string]struct {
		dir       string
		name      string
		source    string
		neighbors []string
		want      []string
	}{
		"marker literal outside the launch module": {
			dir: "model", name: "keys.go",
			source: `package model
func (m Model) start() {
	ctx := actions.AgentLaunchContext{LaunchID: "l", FlowRepair: true}
	_ = ctx
}`,
			want: []string{"Model.start.FlowRepair"},
		},
		"marker literal inside the launch module": {
			dir: "model", name: flowSeamLaunchContextExemptFile,
			source: `package model
func build() {
	_ = actions.AgentLaunchContext{FlowID: "f", FlowPhaseID: "p"}
}`,
			want: []string{},
		},
		"launch context without a marker": {
			dir: "model", name: "keys.go",
			source: `package model
func plain() {
	_ = actions.AgentLaunchContext{LaunchID: "l", Command: "sh"}
}`,
			want: []string{},
		},
		"marker literal inside a closure": {
			dir: "model", name: "keys.go",
			source: `package model
func (m Model) outer() tea.Cmd {
	return func() tea.Msg {
		return actions.AgentLaunchContext{FlowAgent: true}
	}
}`,
			want: []string{"Model.outer.FlowAgent"},
		},
		"marker assigned after the literal": {
			dir: "model", name: "keys.go",
			source: `package model
func later() {
	ctx := actions.AgentLaunchContext{LaunchID: "l"}
	ctx.FlowAutoLaunch = true
}`,
			want: []string{"later.FlowAutoLaunch"},
		},
		"marker assigned on a launch context parameter": {
			dir: "model", name: "keys.go",
			source: `package model
func stamp(ctx actions.AgentLaunchContext) {
	ctx.FlowPhaseTerminal = true
}`,
			want: []string{"stamp.FlowPhaseTerminal"},
		},
		"same-named field on an unrelated struct": {
			dir: "model", name: "keys.go",
			source: `package model
func unrelated() {
	attempt := flowLaunchAttempt{}
	attempt.FlowID = "f"
}`,
			want: []string{},
		},
		"bare ident literal used inside actions": {
			dir: "actions", name: "actions.go",
			source: `package actions
func build() AgentLaunchContext {
	return AgentLaunchContext{FlowSavedSessionResume: true}
}`,
			want: []string{"build.FlowSavedSessionResume"},
		},
		"marker literal at package level": {
			dir: "model", name: "keys.go",
			source: `package model
var seed = actions.AgentLaunchContext{FlowAutofix: true}`,
			want: []string{".FlowAutofix"},
		},
		"marker assigned through an alias of a launch context": {
			dir: "model", name: "keys.go",
			source: `package model
func aliased(ctx actions.AgentLaunchContext) {
	alias := ctx
	alias.FlowRepair = true
}`,
			want: []string{"aliased.FlowRepair"},
		},
		"marker assigned through a launch context struct field": {
			dir: "model", name: "messages.go",
			source: `package model
type launchMsg struct {
	LaunchContext actions.AgentLaunchContext
}

func nested(msg launchMsg) {
	msg.LaunchContext.FlowPhaseKind = "review"
}`,
			want: []string{"nested.FlowPhaseKind"},
		},
		"marker assigned through a pointer dereference": {
			dir: "model", name: "keys.go",
			source: `package model
func deref(ctx *actions.AgentLaunchContext) {
	(*ctx).FlowLaunchTracked = true
}`,
			want: []string{"deref.FlowLaunchTracked"},
		},
		"marker assigned on an alias of a launch context field": {
			dir: "model", name: "messages.go",
			source: `package model
type launchMsg struct {
	LaunchContext actions.AgentLaunchContext
}

func copied(msg launchMsg) {
	ctx := msg.LaunchContext
	ctx.FlowID = "f"
}`,
			want: []string{"copied.FlowID"},
		},
		"marker literal behind an aliased actions import": {
			dir: "model", name: "keys.go",
			source: `package model

import act "github.com/approachcontrol/approach/actions"

func aliasedImport() {
	_ = act.AgentLaunchContext{FlowRepair: true}
}`,
			want: []string{"aliasedImport.FlowRepair"},
		},
		"same-named type from an unrelated package": {
			dir: "model", name: "keys.go",
			source: `package model

import act "github.com/elsewhere/other"

func unrelatedPackage() {
	_ = act.AgentLaunchContext{FlowRepair: true}
}`,
			want: []string{},
		},
		"marker assigned on a helper-returned launch context": {
			dir: "model", name: "keys.go",
			source: `package model
func makeContext() actions.AgentLaunchContext {
	return actions.AgentLaunchContext{LaunchID: "l"}
}

func viaHelper() {
	ctx := makeContext()
	ctx.FlowRepair = true
}`,
			want: []string{"viaHelper.FlowRepair"},
		},
		"marker assigned on a multi-result helper context": {
			dir: "model", name: "keys.go",
			source: `package model
func buildContext() (flowLaunchRouteDecision, actions.AgentLaunchContext, error) {
	return flowLaunchRouteDecision{}, actions.AgentLaunchContext{}, nil
}

func viaMultiResult() {
	decision, ctx, err := buildContext()
	_, _ = decision, err
	ctx.FlowAgent = true
}`,
			want: []string{"viaMultiResult.FlowAgent"},
		},
		"marker assigned on a result of a helper returning something else": {
			dir: "model", name: "keys.go",
			source: `package model
func makeAttempt() flowLaunchAttempt {
	return flowLaunchAttempt{}
}

func viaOtherHelper() {
	attempt := makeAttempt()
	attempt.FlowID = "f"
}`,
			want: []string{},
		},
		"same-named field on an unrelated nested struct": {
			dir: "model", name: "keys.go",
			source: `package model
type decoy struct {
	LaunchContext otherpkg.Context
}

func unrelatedNested(d decoy) {
	d.LaunchContext.FlowID = "f"
}`,
			want: []string{},
		},
		"closure shadowing does not hide the outer marker write": {
			dir: "model", name: "keys.go",
			source: `package model
type decoy struct{}

func shadowed(ctx actions.AgentLaunchContext) func() {
	ctx.FlowRepair = true
	return func() {
		ctx := decoy{}
		_ = ctx
	}
}`,
			want: []string{"shadowed.FlowRepair"},
		},
		"unrelated struct sharing a real launch context field name": {
			dir: "model", name: "keys.go",
			source: `package model
type decoy struct {
	LaunchContext otherpkg.Context
}

func unrelatedOwner(d decoy) {
	d.LaunchContext.FlowID = "f"
}`,
			neighbors: []string{`package model
type launchMsg struct {
	LaunchContext actions.AgentLaunchContext
}`},
			want: []string{},
		},
		"marker assigned through a slice element": {
			dir: "model", name: "keys.go",
			source: `package model
func viaSlice() {
	contexts := []actions.AgentLaunchContext{{}}
	contexts[0].FlowRepair = true
}`,
			want: []string{"viaSlice.FlowRepair"},
		},
		"marker keyed in a slice element literal": {
			dir: "model", name: "keys.go",
			source: `package model
func inSliceLiteral() {
	_ = []actions.AgentLaunchContext{{FlowAgent: true}}
}`,
			want: []string{"inSliceLiteral.FlowAgent"},
		},
		"marker assigned through a map element": {
			dir: "model", name: "keys.go",
			source: `package model
func viaMap(contexts map[string]actions.AgentLaunchContext) {
	contexts["a"].FlowPhaseID = "p"
}`,
			want: []string{"viaMap.FlowPhaseID"},
		},
		"marker assigned on a range value": {
			dir: "model", name: "keys.go",
			source: `package model
func viaRange(contexts []actions.AgentLaunchContext) {
	for _, ctx := range contexts {
		ctx.FlowAutofixPRNumber = 1
	}
}`,
			want: []string{"viaRange.FlowAutofixPRNumber"},
		},
		"marker assigned through a struct field holding launch contexts": {
			dir: "model", name: "keys.go",
			source: `package model
func viaFieldSlice(batch launchBatch) {
	batch.Contexts[0].FlowID = "f"
}`,
			neighbors: []string{`package model
type launchBatch struct {
	Contexts []actions.AgentLaunchContext
}`},
			want: []string{"viaFieldSlice.FlowID"},
		},
		"builder-wrapped literal is still inspected": {
			dir: "model", name: "ready_bead_slice.go",
			source: `package model
func slice() {
	ctx := applyLaunchStamp(actions.AgentLaunchContext{LaunchID: "l"})
	ctx.FlowID = "f"
}`,
			want: []string{"slice.FlowID"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := flowSeamViolationsForSource(t, test.dir, test.name, test.source, test.neighbors...)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("violations = %v, want %v", got, test.want)
			}
		})
	}
}

// flowSeamAllowance names a function outside the launch module that may set
// Flow markers on a launch context.
type flowSeamAllowance struct {
	Dir  string
	File string
	Func string
}

// flowSeamAllowList is keyed by function rather than by file:line so that an
// unrelated edit above the literal does not re-pin the guard. Each of these
// addresses a launch record that already exists; neither starts a process, so
// neither is a launch the role builder could have built.
//
// The four remaining out-of-module AgentLaunchContext constructors — the
// Ready-Bead `S` slice, plans-mode implement, worktree `a`, and non-Flow
// session resume — need no entry: they set no Flow marker at all, which is
// exactly what keeps flowLaunchContextRequiresLifecycle false for them. An
// allow-list entry there would be dead configuration granting a standing
// exemption, so the stale-entry check below would rightly fail on it.
var flowSeamAllowList = map[flowSeamAllowance]string{
	{Dir: "model", File: "flow_launch_lifecycle.go", Func: "Model.blockAutoFlowLaunchPhase"}:   "Synthesizes a failure context addressing the launch attempt that already failed; there is no process to start.",
	{Dir: "model", File: "flow_session_release.go", Func: "Model.releaseFlowPhaseSessionsCmd"}: "Finalizes the hook records of launches that already ran and are being released.",
}

// flowSeamScanRoot is the repository root, reached from the model package.
const flowSeamScanRoot = ".."

// flowSeamSkippedDirs are directories with no Go of ours in them. `web` is the
// separate Next.js deployable; the rest are tool and dependency output.
var flowSeamSkippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"web":          true,
}

// flowSeamGoFile is one non-test Go file in the scan set, named the way a
// violation reports it: package directory relative to the repository root,
// plus file name.
type flowSeamGoFile struct {
	Dir  string
	Name string
	Path string
}

// flowSeamScanFiles walks every non-test Go file in the repository.
// AgentLaunchContext is exported, so any package can build one; scanning only
// the two packages that happen to do so today would let the next one in.
func flowSeamScanFiles(t *testing.T) []flowSeamGoFile {
	t.Helper()
	var files []flowSeamGoFile
	err := filepath.WalkDir(flowSeamScanRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != flowSeamScanRoot && (strings.HasPrefix(name, ".") || flowSeamSkippedDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		dir = strings.TrimPrefix(strings.TrimPrefix(dir, flowSeamScanRoot), "/")
		if dir == "" {
			dir = "."
		}
		files = append(files, flowSeamGoFile{Dir: dir, Name: name, Path: path})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", flowSeamScanRoot, err)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Dir != files[j].Dir {
			return files[i].Dir < files[j].Dir
		}
		return files[i].Name < files[j].Name
	})
	return files
}

// TestFlowLaunchContextLiteralsStayInsideTheLaunchModule is the enforcement:
// no Flow marker is set on a launch context outside
// model/flow_launch_context.go, save for the two allow-listed finalizers.
func TestFlowLaunchContextLiteralsStayInsideTheLaunchModule(t *testing.T) {
	markers := flowSeamMarkerFields()
	fset := token.NewFileSet()
	matched := make(map[flowSeamAllowance]bool)
	var offenders []string

	// Two passes: the struct fields and helper results that carry a launch
	// context are declared in one file and used in another, so the whole scan
	// set has to be parsed before any of it can be judged.
	type flowSeamSourceFile struct {
		dir  string
		name string
		file *ast.File
	}
	var sources []flowSeamSourceFile
	shapes := newFlowSeamContextShapes()
	scanned := flowSeamScanFiles(t)
	if len(scanned) == 0 {
		t.Fatalf("no Go files found under %s; the seam rule is scanning nothing", flowSeamScanRoot)
	}
	for _, source := range scanned {
		file, err := parser.ParseFile(fset, source.Path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source.Path, err)
		}
		sources = append(sources, flowSeamSourceFile{dir: source.Dir, name: source.Name, file: file})
		shapes.merge(flowSeamContextShapesOf(file))
	}
	beyondTheLaunchPackages := false
	for _, source := range scanned {
		if source.Dir != "model" && source.Dir != "actions" {
			beyondTheLaunchPackages = true
			break
		}
	}
	if !beyondTheLaunchPackages {
		t.Fatal("the scan found nothing outside model/ and actions/; AgentLaunchContext is exported, so the walk has to reach every package")
	}
	if shapes.contextFieldCount() == 0 {
		t.Fatal("no struct field of type actions.AgentLaunchContext found; the nested-field half of the seam rule is scanning nothing")
	}
	if len(shapes.results) == 0 {
		t.Fatal("no function returning an actions.AgentLaunchContext found; the helper-result half of the seam rule is scanning nothing")
	}
	for _, source := range sources {
		for _, violation := range flowSeamViolations(source.dir, source.name, source.file, markers, shapes) {
			allowance := flowSeamAllowance{Dir: violation.Dir, File: violation.File, Func: violation.Func}
			if _, allowed := flowSeamAllowList[allowance]; allowed {
				matched[allowance] = true
				continue
			}
			offenders = append(offenders, violation.String())
		}
	}
	sort.Strings(offenders)
	for _, offender := range offenders {
		t.Errorf("Flow launch context built outside the launch module: %s", offender)
	}
	if len(offenders) > 0 {
		t.Log("build Flow launch contexts through the role builder in model/" + flowSeamLaunchContextExemptFile)
	}
	for allowance := range flowSeamAllowList {
		if !matched[allowance] {
			t.Errorf("stale Flow seam allow-list entry: %s/%s %s sets no Flow marker; delete the entry",
				allowance.Dir, allowance.File, allowance.Func)
		}
	}
}

// TestFlowLaunchLifecycleBackstopStaysWired keeps the runtime half of the
// guard from being deleted once the static half exists: launchAgentForBackend
// must still refuse a marker-bearing context that reaches it anyway.
func TestFlowLaunchLifecycleBackstopStaysWired(t *testing.T) {
	contracts := parseFlowLaunchFunctionContracts(t, false)
	contract, ok := contracts[modelFlowLaunchFunction("launchAgentForBackend")]
	if !ok {
		t.Fatal("Model.launchAgentForBackend is gone; the Flow launch runtime backstop lost its home")
	}
	if !contract.calls["flowLaunchContextRequiresLifecycle"] {
		t.Fatal("Model.launchAgentForBackend no longer calls flowLaunchContextRequiresLifecycle; the static seam test is not a substitute for the runtime refusal")
	}
}
