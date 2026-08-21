package model

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
// flowLaunchContextRequiresLifecycle stays the runtime backstop for whatever a
// syntactic rule cannot see (aliased imports, contexts threaded through
// helpers). This test is the earlier, louder half of the same guard.

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

// flowSeamIsLaunchContextType matches the two syntactic spellings of the type:
// actions.AgentLaunchContext from model, and the bare ident from inside
// actions itself.
func flowSeamIsLaunchContextType(expr ast.Expr) bool {
	switch typ := expr.(type) {
	case *ast.Ident:
		return typ.Name == "AgentLaunchContext"
	case *ast.StarExpr:
		return flowSeamIsLaunchContextType(typ.X)
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		return ok && pkg.Name == "actions" && typ.Sel.Name == "AgentLaunchContext"
	}
	return false
}

// flowSeamLaunchContextLit unwraps the expressions that still yield a launch
// context: parentheses, an address-of, and applyLaunchStamp, which the builder
// roles wrap their literals in. Anything else returns nil, because an
// assignment target has to be *provably* a launch context before a marker
// write on it counts as a violation.
func flowSeamLaunchContextLit(expr ast.Expr) *ast.CompositeLit {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamLaunchContextLit(expr.X)
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return flowSeamLaunchContextLit(expr.X)
		}
	case *ast.CallExpr:
		if fn, ok := expr.Fun.(*ast.Ident); ok && fn.Name == "applyLaunchStamp" && len(expr.Args) > 0 {
			return flowSeamLaunchContextLit(expr.Args[0])
		}
	case *ast.CompositeLit:
		if flowSeamIsLaunchContextType(expr.Type) {
			return expr
		}
	}
	return nil
}

// flowSeamContextFieldNames collects every struct field in file whose declared
// type is an AgentLaunchContext. A marker written through such a field —
// `msg.LaunchContext.FlowRepair = true` — builds Flow launch identity just as
// surely as a write to a local, so the detector needs the field names to tell
// those apart from a same-named field on an unrelated struct.
func flowSeamContextFieldNames(file *ast.File) map[string]bool {
	fields := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		structType, ok := n.(*ast.StructType)
		if !ok || structType.Fields == nil {
			return true
		}
		for _, field := range structType.Fields.List {
			if !flowSeamIsLaunchContextType(field.Type) {
				continue
			}
			for _, name := range field.Names {
				fields[name.Name] = true
			}
		}
		return true
	})
	return fields
}

// flowSeamLaunchContextExpr reports whether expr provably yields a launch
// context: a literal, a name already known to hold one, or a read of a struct
// field declared as one, through any number of parentheses, address-of, and
// dereference wrappers.
func flowSeamLaunchContextExpr(expr ast.Expr, names, fields map[string]bool) bool {
	if flowSeamLaunchContextLit(expr) != nil {
		return true
	}
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamLaunchContextExpr(expr.X, names, fields)
	case *ast.StarExpr:
		return flowSeamLaunchContextExpr(expr.X, names, fields)
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return flowSeamLaunchContextExpr(expr.X, names, fields)
		}
	case *ast.Ident:
		return names[expr.Name]
	case *ast.SelectorExpr:
		return fields[expr.Sel.Name]
	}
	return false
}

// flowSeamLaunchContextNames collects every identifier in node that is
// provably an AgentLaunchContext: parameters and results of the function and
// of any closure inside it, `var` declarations, and bindings of a launch
// context literal, of another such name, or of a launch context struct field.
// Aliases chain, so the walk repeats until it stops learning names.
func flowSeamLaunchContextNames(node ast.Node, fields map[string]bool) map[string]bool {
	names := make(map[string]bool)
	for {
		before := len(names)
		flowSeamCollectLaunchContextNames(node, names, fields)
		if len(names) == before {
			return names
		}
	}
}

func flowSeamCollectLaunchContextNames(node ast.Node, names, fields map[string]bool) {
	addFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			if !flowSeamIsLaunchContextType(field.Type) {
				continue
			}
			for _, name := range field.Names {
				names[name.Name] = true
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
		case *ast.FuncLit:
			addFuncType(n.Type)
		case *ast.ValueSpec:
			if flowSeamIsLaunchContextType(n.Type) {
				for _, name := range n.Names {
					names[name.Name] = true
				}
			}
			for index, value := range n.Values {
				if index < len(n.Names) && flowSeamLaunchContextExpr(value, names, fields) {
					names[n.Names[index].Name] = true
				}
			}
		case *ast.AssignStmt:
			for index, value := range n.Rhs {
				if index >= len(n.Lhs) || !flowSeamLaunchContextExpr(value, names, fields) {
					continue
				}
				if ident, ok := n.Lhs[index].(*ast.Ident); ok {
					names[ident.Name] = true
				}
			}
		}
		return true
	})
}

// flowSeamNodeViolations reports marker fields set within one declaration:
// keys on a launch context literal, and assignments to a marker field of a
// name that is provably a launch context.
func flowSeamNodeViolations(dir, file, function string, node ast.Node, markers, fields map[string]bool) []flowSeamViolation {
	locals := flowSeamLaunchContextNames(node, fields)
	var violations []flowSeamViolation
	record := func(field string) {
		violations = append(violations, flowSeamViolation{Dir: dir, File: file, Func: function, Field: field})
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			if !flowSeamIsLaunchContextType(n.Type) {
				return true
			}
			for _, element := range n.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if ok && markers[key.Name] {
					record(key.Name)
				}
			}
		case *ast.AssignStmt:
			for _, target := range n.Lhs {
				selector, ok := target.(*ast.SelectorExpr)
				if !ok || !markers[selector.Sel.Name] {
					continue
				}
				if flowSeamLaunchContextExpr(selector.X, locals, fields) {
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
func flowSeamViolations(dir, name string, file *ast.File, markers, fields map[string]bool) []flowSeamViolation {
	if dir == "model" && name == flowSeamLaunchContextExemptFile {
		return nil
	}
	var violations []flowSeamViolation
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			violations = append(violations, flowSeamNodeViolations(dir, name, "", declaration, markers, fields)...)
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
		violations = append(violations, flowSeamNodeViolations(dir, name, label, function, markers, fields)...)
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

func flowSeamViolationsForSource(t *testing.T, dir, name, source string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	found := flowSeamViolations(dir, name, file, flowSeamMarkerFields(), flowSeamContextFieldNames(file))
	reported := make([]string, 0, len(found))
	for _, violation := range found {
		reported = append(reported, violation.Func+"."+violation.Field)
	}
	sort.Strings(reported)
	return reported
}

func TestFlowSeamDetectorFindsOutOfModuleMarkers(t *testing.T) {
	tests := map[string]struct {
		dir    string
		name   string
		source string
		want   []string
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
			got := flowSeamViolationsForSource(t, test.dir, test.name, test.source)
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

func flowSeamScanDirs() map[string]string {
	return map[string]string{"model": ".", "actions": "../actions"}
}

// TestFlowLaunchContextLiteralsStayInsideTheLaunchModule is the enforcement:
// no Flow marker is set on a launch context outside
// model/flow_launch_context.go, save for the two allow-listed finalizers.
func TestFlowLaunchContextLiteralsStayInsideTheLaunchModule(t *testing.T) {
	markers := flowSeamMarkerFields()
	fset := token.NewFileSet()
	matched := make(map[flowSeamAllowance]bool)
	var offenders []string

	// Two passes: the struct fields that hold a launch context are declared in
	// one file and written through in another, so the whole scan set has to be
	// parsed before any of it can be judged.
	type flowSeamSourceFile struct {
		dir  string
		name string
		file *ast.File
	}
	var sources []flowSeamSourceFile
	fields := make(map[string]bool)
	for _, dir := range sortedKeys(flowSeamScanDirs()) {
		path := flowSeamScanDirs()[dir]
		entries, err := os.ReadDir(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, filepath.Join(path, name), nil, 0)
			if err != nil {
				t.Fatalf("parse %s/%s: %v", dir, name, err)
			}
			sources = append(sources, flowSeamSourceFile{dir: dir, name: name, file: file})
			for field := range flowSeamContextFieldNames(file) {
				fields[field] = true
			}
		}
	}
	if len(fields) == 0 {
		t.Fatal("no struct field of type actions.AgentLaunchContext found; the nested-field half of the seam rule is scanning nothing")
	}
	for _, source := range sources {
		for _, violation := range flowSeamViolations(source.dir, source.name, source.file, markers, fields) {
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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
