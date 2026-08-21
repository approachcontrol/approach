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

// flowSeamModulePath is this repository's module path, and
// flowSeamActionsImportPath the package the launch context type lives in.
const (
	flowSeamModulePath         = "github.com/approachcontrol/approach"
	flowSeamActionsImportPath  = flowSeamModulePath + "/actions"
	flowSeamLaunchContextIdent = "AgentLaunchContext"
)

// flowSeamFile is everything about one file that a type expression in it has
// to be read against.
type flowSeamFile struct {
	// dir is the file's package directory, relative to the repository root.
	// It qualifies every named type the file declares or spells, so two
	// packages that happen to share a struct name stay distinct.
	dir string
	// pkgs are the identifiers that name the actions package here, since
	// `import act ".../actions"` spells the same type `act.AgentLaunchContext`.
	pkgs map[string]bool
	// imports maps a package identifier to the repository directory it refers
	// to, for imports of this module, so `msgs.Envelope` qualifies too.
	imports map[string]string
	// aliases are the qualified names of declared aliases of the launch
	// context — `type launchContext = actions.AgentLaunchContext` — which are
	// that exact type and so must classify as one. Collected across the whole
	// scan set, since an alias is declared in one file and used in another.
	aliases map[string]bool
}

// flowSeamQualify names a type by the package directory that declares it.
func flowSeamQualify(dir, name string) string {
	if dir == "" || name == "" {
		return ""
	}
	return dir + "." + name
}

// flowSeamDirForImportPath maps an import path of this module to the
// repository directory it lives in, and returns "" for anything external.
func flowSeamDirForImportPath(path string) string {
	if path == flowSeamModulePath {
		return "."
	}
	if !strings.HasPrefix(path, flowSeamModulePath+"/") {
		return ""
	}
	return strings.TrimPrefix(path, flowSeamModulePath+"/")
}

// flowSeamFileScope reads a file's import declarations: which identifiers name
// the actions package, and which name some other package of this module. The
// unaliased "actions" is always accepted so a source fragment without imports
// still reads normally.
func flowSeamFileScope(dir string, file *ast.File, aliases map[string]bool) flowSeamFile {
	scope := flowSeamFile{
		dir:     dir,
		pkgs:    map[string]bool{"actions": true},
		imports: make(map[string]string),
		aliases: aliases,
	}
	if scope.aliases == nil {
		scope.aliases = make(map[string]bool)
	}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		if imported.Name != nil {
			name = imported.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		if name == "" {
			name = filepath.Base(path)
		}
		if path == flowSeamActionsImportPath {
			scope.pkgs[name] = true
		}
		if importedDir := flowSeamDirForImportPath(path); importedDir != "" {
			scope.imports[name] = importedDir
		}
	}
	return scope
}

// flowSeamIsLaunchContextType matches every spelling of the type: the bare
// ident inside the actions package itself, a qualified name under whatever the
// file imports the actions package as, and any declared alias of either. A
// package that declares its own type of the same name is not this type.
func flowSeamIsLaunchContextType(expr ast.Expr, file flowSeamFile) bool {
	switch typ := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamIsLaunchContextType(typ.X, file)
	case *ast.StarExpr:
		return flowSeamIsLaunchContextType(typ.X, file)
	case *ast.Ident:
		// The bare spelling is the type only inside actions itself; elsewhere
		// a package-local type of the same name is somebody else's.
		if typ.Name == flowSeamLaunchContextIdent && file.dir == "actions" {
			return true
		}
		return file.aliases[flowSeamQualify(file.dir, typ.Name)]
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		if !ok {
			return false
		}
		if file.pkgs[pkg.Name] && typ.Sel.Name == flowSeamLaunchContextIdent {
			return true
		}
		return file.aliases[flowSeamQualify(file.imports[pkg.Name], typ.Sel.Name)]
	}
	return false
}

// flowSeamAliasNames collects the qualified names this file declares as an
// alias of the launch context. Aliases can chain, so the caller repeats the
// pass until it stops learning names.
func flowSeamAliasNames(dir string, file *ast.File, known map[string]bool) map[string]bool {
	scope := flowSeamFileScope(dir, file, known)
	found := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || !spec.Assign.IsValid() {
			return true
		}
		if flowSeamIsLaunchContextType(spec.Type, scope) {
			found[flowSeamQualify(dir, spec.Name.Name)] = true
		}
		return true
	})
	return found
}

// flowSeamLaunchContextLit unwraps the expressions that still yield a launch
// context: parentheses, an address-of, and applyLaunchStamp, which the builder
// roles wrap their literals in. Anything else returns nil, because an
// assignment target has to be *provably* a launch context before a marker
// write on it counts as a violation.
func flowSeamLaunchContextLit(expr ast.Expr, file flowSeamFile) *ast.CompositeLit {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamLaunchContextLit(expr.X, file)
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return flowSeamLaunchContextLit(expr.X, file)
		}
	case *ast.CallExpr:
		if fn, ok := expr.Fun.(*ast.Ident); ok && fn.Name == "applyLaunchStamp" && len(expr.Args) > 0 {
			return flowSeamLaunchContextLit(expr.Args[0], file)
		}
	case *ast.CompositeLit:
		if flowSeamIsLaunchContextType(expr.Type, file) {
			return expr
		}
	}
	return nil
}

// flowSeamKind is what an expression was found to yield: a launch context, a
// slice or map of them, and/or a value of a named type declared in this
// repository, qualified by the package that declares it. The named type is
// what lets a field read be judged by its owner rather than by field name
// alone.
type flowSeamKind struct {
	context    bool
	collection bool
	named      string
	// element is the qualified named type a slice or map holds, for
	// `type wrapped []Base`, where whether this is a collection of contexts
	// is only answerable once Base itself has been resolved.
	element string
}

func (k flowSeamKind) known() bool {
	return k.context || k.collection || k.named != "" || k.element != ""
}

// flowSeamTypeKind classifies a type expression: an AgentLaunchContext, a
// slice/array/map/variadic of them, or a named type of this repository.
func flowSeamTypeKind(expr ast.Expr, file flowSeamFile) flowSeamKind {
	if flowSeamIsLaunchContextType(expr, file) {
		return flowSeamKind{context: true}
	}
	switch typ := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamTypeKind(typ.X, file)
	case *ast.StarExpr:
		return flowSeamTypeKind(typ.X, file)
	case *ast.Ellipsis:
		return flowSeamElementKind(typ.Elt, file)
	case *ast.ArrayType:
		return flowSeamElementKind(typ.Elt, file)
	case *ast.MapType:
		return flowSeamElementKind(typ.Value, file)
	case *ast.Ident:
		return flowSeamKind{named: flowSeamQualify(file.dir, typ.Name)}
	case *ast.SelectorExpr:
		if pkg, ok := typ.X.(*ast.Ident); ok {
			return flowSeamKind{named: flowSeamQualify(file.imports[pkg.Name], typ.Sel.Name)}
		}
	}
	return flowSeamKind{}
}

// flowSeamElementKind classifies what a slice, array, or map holds: contexts
// outright, or a named type that only the collected definitions can resolve.
func flowSeamElementKind(expr ast.Expr, file flowSeamFile) flowSeamKind {
	if flowSeamIsLaunchContextType(expr, file) {
		return flowSeamKind{collection: true}
	}
	if element := flowSeamTypeKind(expr, file); element.named != "" {
		return flowSeamKind{element: element.named}
	}
	return flowSeamKind{}
}

// flowSeamContextShapes is what the scan set has to be read for before any
// marker write in it can be judged: which field of which struct holds a launch
// context, and which functions hand one back. Both are declared in one file
// and used in another, so they are merged across every scanned file first.
type flowSeamContextShapes struct {
	// fields is qualified owner type -> field name -> what that field holds,
	// so a write through one — `msg.LaunchContext.FlowRepair = true` — is
	// judged by the struct it belongs to. Qualifying the owner by package is
	// what keeps an unrelated struct that happens to share a name and a
	// `LaunchContext` field from either failing the guard or hiding a real
	// violation.
	fields map[string]map[string]flowSeamKind
	// results maps a function name to what each of its results holds, named
	// types included so a helper handing back a wrapper resolves like one, so
	// `ctx := makeContext(); ctx.FlowRepair = true` is caught the same way a
	// literal binding is, and a helper handing back a slice or map of contexts
	// is followed into its elements. Receivers and packages are ignored here:
	// a call is resolved by name only, and conflating two same-named functions
	// only makes the seam rule stricter.
	results map[string]map[int]flowSeamKind
	// aliases are the qualified names declared as an alias of the launch
	// context anywhere in the scan set.
	aliases map[string]bool
	// underlying is qualified named type -> what defining it actually made,
	// for `type launchContexts []actions.AgentLaunchContext` and friends. A
	// defined type has the launch context's fields, so a marker write through
	// one is a marker write.
	underlying map[string]flowSeamKind
	// embeds is qualified owner type -> what it embeds anonymously, either
	// the launch context itself (under flowSeamEmbeddedContext) or another
	// qualified type. Go promotes an embedded context's fields, so
	// `type wrapper struct{ actions.AgentLaunchContext }` makes
	// `w.FlowID = "f"` a marker write on a launch context.
	embeds map[string]map[string]bool
}

// flowSeamScope pairs the scan-set-wide shapes with the file the expressions
// being judged were written in.
type flowSeamScope struct {
	file   flowSeamFile
	shapes flowSeamContextShapes
}

func newFlowSeamContextShapes() flowSeamContextShapes {
	return flowSeamContextShapes{
		fields:  make(map[string]map[string]flowSeamKind),
		results: make(map[string]map[int]flowSeamKind),
		aliases: make(map[string]bool),
		embeds:  make(map[string]map[string]bool),

		underlying: make(map[string]flowSeamKind),
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
	for name, kinds := range other.results {
		if s.results[name] == nil {
			s.results[name] = make(map[int]flowSeamKind)
		}
		for index, kind := range kinds {
			if flowSeamKindRank(kind) > flowSeamKindRank(s.results[name][index]) {
				s.results[name][index] = kind
			}
		}
	}
	for alias := range other.aliases {
		s.aliases[alias] = true
	}
	for named, kind := range other.underlying {
		if flowSeamKindRank(kind) > flowSeamKindRank(s.underlying[named]) {
			s.underlying[named] = kind
		}
	}
	for owner, embedded := range other.embeds {
		if s.embeds[owner] == nil {
			s.embeds[owner] = make(map[string]bool)
		}
		for name := range embedded {
			s.embeds[owner][name] = true
		}
	}
}

// flowSeamEmbeddedContext is the embeds entry for a struct that embeds the
// launch context itself rather than another named type.
const flowSeamEmbeddedContext = "<launch context>"

// promotesContext reports whether owner has the launch context's fields
// promoted into it, directly or through a chain of embedded types.
func (s flowSeamContextShapes) promotesContext(owner string, seen map[string]bool) bool {
	if owner == "" || seen[owner] {
		return false
	}
	seen[owner] = true
	for embedded := range s.embeds[owner] {
		if embedded == flowSeamEmbeddedContext || s.promotesContext(embedded, seen) {
			return true
		}
	}
	return false
}

// resolveKind follows a named type to what defining it actually made, so
// `type launchContexts []actions.AgentLaunchContext` reads as a collection
// wherever one of its values appears.
func (scope flowSeamScope) resolveKind(kind flowSeamKind) flowSeamKind {
	return scope.resolveKindSeen(kind, make(map[string]bool))
}

func (scope flowSeamScope) resolveKindSeen(kind flowSeamKind, seen map[string]bool) flowSeamKind {
	for {
		switch {
		case kind.context, kind.collection:
			return kind
		case kind.element != "":
			element := scope.resolveNamed(kind.element, seen)
			if element.context {
				return flowSeamKind{collection: true}
			}
			return kind
		case kind.named != "" && !seen[kind.named]:
			seen[kind.named] = true
			resolved, ok := scope.shapes.underlying[kind.named]
			if !ok {
				return kind
			}
			kind = resolved
		default:
			return kind
		}
	}
}

// resolveNamed follows a qualified named type to what defining it made.
func (scope flowSeamScope) resolveNamed(name string, seen map[string]bool) flowSeamKind {
	if seen[name] {
		return flowSeamKind{}
	}
	seen[name] = true
	resolved, ok := scope.shapes.underlying[name]
	if !ok {
		return flowSeamKind{}
	}
	return scope.resolveKindSeen(resolved, seen)
}

// holdsContext reports whether a marker write on a value of this kind writes a
// launch context: the value is one, was defined as one, or embeds one and has
// its fields promoted.
func (scope flowSeamScope) holdsContext(kind flowSeamKind) bool {
	if scope.resolveKind(kind).context {
		return true
	}
	return scope.shapes.promotesContext(kind.named, make(map[string]bool))
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

// resultKinds reports what each result of the called function holds, for a
// call whose callee is a plain name or a method selector.
func (s flowSeamContextShapes) resultKinds(call *ast.CallExpr) map[int]flowSeamKind {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return s.results[fn.Name]
	case *ast.SelectorExpr:
		return s.results[fn.Sel.Name]
	}
	return nil
}

// flowSeamEmbeddedName is the field name Go gives an anonymous field: the base
// name of its type.
func flowSeamEmbeddedName(expr ast.Expr) string {
	switch typ := expr.(type) {
	case *ast.ParenExpr:
		return flowSeamEmbeddedName(typ.X)
	case *ast.StarExpr:
		return flowSeamEmbeddedName(typ.X)
	case *ast.Ident:
		return typ.Name
	case *ast.SelectorExpr:
		return typ.Sel.Name
	}
	return ""
}

func flowSeamContextShapesOf(dir string, file *ast.File, aliases map[string]bool) flowSeamContextShapes {
	shapes := newFlowSeamContextShapes()
	for alias := range aliases {
		shapes.aliases[alias] = true
	}
	scope := flowSeamFileScope(dir, file, aliases)
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.TypeSpec:
			if !n.Assign.IsValid() {
				if kind := flowSeamTypeKind(n.Type, scope); kind.known() {
					shapes.underlying[flowSeamQualify(dir, n.Name.Name)] = kind
				}
			}
			structType, ok := n.Type.(*ast.StructType)
			if !ok || structType.Fields == nil {
				return true
			}
			owner := flowSeamQualify(dir, n.Name.Name)
			for _, field := range structType.Fields.List {
				kind := flowSeamTypeKind(field.Type, scope)
				if !kind.known() {
					continue
				}
				names := make([]string, 0, len(field.Names))
				for _, name := range field.Names {
					names = append(names, name.Name)
				}
				if len(names) == 0 {
					// An embedded field is named after its type, and its own
					// fields are promoted into the embedding struct.
					embeddedName := flowSeamEmbeddedName(field.Type)
					if embeddedName == "" {
						continue
					}
					names = append(names, embeddedName)
					if shapes.embeds[owner] == nil {
						shapes.embeds[owner] = make(map[string]bool)
					}
					if kind.context {
						shapes.embeds[owner][flowSeamEmbeddedContext] = true
					} else if kind.named != "" {
						shapes.embeds[owner][kind.named] = true
					}
				}
				for _, name := range names {
					if shapes.fields[owner] == nil {
						shapes.fields[owner] = make(map[string]flowSeamKind)
					}
					shapes.fields[owner][name] = kind
				}
			}
		case *ast.FuncDecl:
			if n.Type == nil || n.Type.Results == nil {
				return true
			}
			kinds := make(map[int]flowSeamKind)
			position := 0
			for _, result := range n.Type.Results.List {
				count := len(result.Names)
				if count == 0 {
					count = 1
				}
				kind := flowSeamTypeKind(result.Type, scope)
				if kind.known() {
					for offset := 0; offset < count; offset++ {
						kinds[position+offset] = kind
					}
				}
				position += count
			}
			if len(kinds) > 0 {
				shapes.results[n.Name.Name] = kinds
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
	case kind.named != "", kind.element != "":
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
	return scope.resolveKind(flowSeamExprKindRaw(expr, locals, scope))
}

func flowSeamExprKindRaw(expr ast.Expr, locals *flowSeamLocals, scope flowSeamScope) flowSeamKind {
	if flowSeamLaunchContextLit(expr, scope.file) != nil {
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
		return flowSeamTypeKind(expr.Type, scope.file)
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
		if kind, ok := scope.shapes.resultKinds(expr)[0]; ok {
			return kind
		}
		// The allocating builtins take the type itself as their first
		// argument: `new(actions.AgentLaunchContext)` and
		// `make([]actions.AgentLaunchContext, n)`.
		if fn, ok := expr.Fun.(*ast.Ident); ok && (fn.Name == "new" || fn.Name == "make") && len(expr.Args) > 0 {
			return flowSeamTypeKind(expr.Args[0], scope.file)
		}
		// A conversion is spelled like a call: `localContext(base)` yields
		// whatever localContext was defined as.
		if len(expr.Args) == 1 {
			return flowSeamTypeKind(expr.Fun, scope.file)
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

// flowSeamBindCallResults records what the names on the left of a single-call
// assignment hold, covering the multi-result `ctx, decision, err := build()`
// shape as well as the single one.
func flowSeamBindCallResults(lhs, rhs []ast.Expr, locals *flowSeamLocals, scope flowSeamScope) {
	if len(rhs) != 1 {
		return
	}
	call, ok := rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}
	kinds := scope.shapes.resultKinds(call)
	for index, target := range lhs {
		if ident, ok := target.(*ast.Ident); ok {
			locals.learn(ident.Name, kinds[index])
		}
	}
}

func flowSeamCollectLaunchContextNames(node ast.Node, locals *flowSeamLocals, scope flowSeamScope) {
	addFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			kind := flowSeamTypeKind(field.Type, scope.file)
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
				kind := flowSeamTypeKind(n.Type, scope.file)
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
			kind := scope.resolveKind(flowSeamTypeKind(n.Type, scope.file))
			if kind.context {
				flowSeamLiteralMarkers(n, markers, record)
				return true
			}
			if !kind.collection {
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
				if scope.holdsContext(flowSeamExprKind(selector.X, locals, scope)) {
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
	scope := flowSeamScope{file: flowSeamFileScope(dir, file, shapes.aliases), shapes: shapes}
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

// flowSeamParsedFile is one parsed source in the scan set, with the package
// directory that qualifies the names it declares.
type flowSeamParsedFile struct {
	dir  string
	name string
	file *ast.File
}

// flowSeamShapesOf reads the whole scan set: aliases of the launch context
// first, repeated until aliases of aliases stop appearing, then the struct
// fields and helper results those aliases let it recognize.
func flowSeamShapesOf(sources []flowSeamParsedFile) flowSeamContextShapes {
	aliases := make(map[string]bool)
	for {
		learned := 0
		for _, source := range sources {
			for alias := range flowSeamAliasNames(source.dir, source.file, aliases) {
				if !aliases[alias] {
					aliases[alias] = true
					learned++
				}
			}
		}
		if learned == 0 {
			break
		}
	}
	shapes := newFlowSeamContextShapes()
	for _, source := range sources {
		shapes.merge(flowSeamContextShapesOf(source.dir, source.file, aliases))
	}
	return shapes
}

// flowSeamViolationsForSource judges source the way the repository scan does:
// shapes merged from source and from every neighbor, then violations read off
// source alone. Neighbors matter because a field declared in one file is
// written through in another, and because a name shared with a real launch
// context field must not turn an unrelated struct into a violation.
// flowSeamNeighbor is another file in the scan set, in whatever package
// directory it belongs to.
type flowSeamNeighbor struct {
	dir    string
	source string
}

func flowSeamViolationsForSource(t *testing.T, dir, name, source string, neighbors ...flowSeamNeighbor) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	sources := []flowSeamParsedFile{{dir: dir, name: name, file: file}}
	for index, neighbor := range neighbors {
		neighborName := fmt.Sprintf("neighbor%d.go", index)
		parsed, err := parser.ParseFile(fset, neighborName, neighbor.source, 0)
		if err != nil {
			t.Fatalf("parse neighbor %d: %v", index, err)
		}
		neighborDir := neighbor.dir
		if neighborDir == "" {
			neighborDir = dir
		}
		sources = append(sources, flowSeamParsedFile{dir: neighborDir, name: neighborName, file: parsed})
	}
	shapes := flowSeamShapesOf(sources)
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
		neighbors []flowSeamNeighbor
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
		"unrelated package declaring its own launch context name": {
			dir: "flowoccupancy", name: "occupancy.go",
			source: `package flowoccupancy
type AgentLaunchContext struct {
	FlowID string
}

func local() {
	_ = AgentLaunchContext{FlowID: "f"}
}`,
			want: []string{},
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
		"marker assigned through a helper-returned collection": {
			dir: "model", name: "keys.go",
			source: `package model
func makeContexts() []actions.AgentLaunchContext {
	return nil
}

func viaHelperCollection() {
	contexts := makeContexts()
	contexts[0].FlowRepair = true
}`,
			want: []string{"viaHelperCollection.FlowRepair"},
		},
		"marker assigned on a context allocated with new": {
			dir: "model", name: "keys.go",
			source: `package model
func viaNew() {
	ctx := new(actions.AgentLaunchContext)
	ctx.FlowRepair = true
}`,
			want: []string{"viaNew.FlowRepair"},
		},
		"marker assigned on a context slice allocated with make": {
			dir: "model", name: "keys.go",
			source: `package model
func viaMake() {
	contexts := make([]actions.AgentLaunchContext, 1)
	contexts[0].FlowID = "f"
}`,
			want: []string{"viaMake.FlowID"},
		},
		"marker assigned on a converted launch context": {
			dir: "model", name: "keys.go",
			source: `package model
type localContext actions.AgentLaunchContext

func viaConversion(base actions.AgentLaunchContext) actions.AgentLaunchContext {
	ctx := localContext(base)
	ctx.FlowRepair = true
	return actions.AgentLaunchContext(ctx)
}`,
			want: []string{"viaConversion.FlowRepair"},
		},
		"marker assigned on a helper-returned embedding wrapper": {
			dir: "model", name: "keys.go",
			source: `package model
type holder struct {
	actions.AgentLaunchContext
}

func makeHolder() holder {
	return holder{}
}

func viaHelperWrapper() {
	h := makeHolder()
	h.FlowID = "f"
}`,
			want: []string{"viaHelperWrapper.FlowID"},
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
			neighbors: []flowSeamNeighbor{{source: `package model
type launchMsg struct {
	LaunchContext actions.AgentLaunchContext
}`}},
			want: []string{},
		},
		"marker assigned on an alias of the launch context": {
			dir: "model", name: "keys.go",
			source: `package model
func viaAlias(ctx launchContext) {
	ctx.FlowRepair = true
}`,
			neighbors: []flowSeamNeighbor{{source: `package model
type launchContext = actions.AgentLaunchContext`}},
			want: []string{"viaAlias.FlowRepair"},
		},
		"marker keyed in a literal of an aliased launch context": {
			dir: "model", name: "keys.go",
			source: `package model
type launchContext = actions.AgentLaunchContext

func inAliasLiteral() {
	_ = launchContext{FlowAutoLaunch: true}
}`,
			want: []string{"inAliasLiteral.FlowAutoLaunch"},
		},
		"owner name shared with another package still reports": {
			dir: "model", name: "keys.go",
			source: `package model
type envelope struct {
	Context actions.AgentLaunchContext
}

func viaSharedOwnerName(e envelope) {
	e.Context.FlowID = "f"
}`,
			neighbors: []flowSeamNeighbor{{dir: "actions", source: `package actions
type envelope struct {
	Context string
}`}},
			want: []string{"viaSharedOwnerName.FlowID"},
		},
		"owner name shared with another package stays unrelated": {
			dir: "actions", name: "actions.go",
			source: `package actions
type envelope struct {
	Context otherpkg.Context
}

func unrelatedSharedOwnerName(e envelope) {
	e.Context.FlowID = "f"
}`,
			neighbors: []flowSeamNeighbor{{dir: "model", source: `package model
type envelope struct {
	Context actions.AgentLaunchContext
}`}},
			want: []string{},
		},
		"marker assigned through a named collection type": {
			dir: "model", name: "keys.go",
			source: `package model
type launchContexts []actions.AgentLaunchContext

func viaNamedCollection(xs launchContexts) {
	xs[0].FlowRepair = true
}`,
			want: []string{"viaNamedCollection.FlowRepair"},
		},
		"marker keyed in a named collection literal": {
			dir: "model", name: "keys.go",
			source: `package model
type launchContexts []actions.AgentLaunchContext

func inNamedCollectionLiteral() {
	_ = launchContexts{{FlowAgent: true}}
}`,
			want: []string{"inNamedCollectionLiteral.FlowAgent"},
		},
		"marker assigned on a defined launch context type": {
			dir: "model", name: "keys.go",
			source: `package model
type definedContext actions.AgentLaunchContext

func viaDefinedType(ctx definedContext) {
	ctx.FlowPhaseTerminal = true
}`,
			want: []string{"viaDefinedType.FlowPhaseTerminal"},
		},
		"marker assigned through a chain of defined context types": {
			dir: "model", name: "keys.go",
			source: `package model
type base actions.AgentLaunchContext

type wrapped base

func viaDefinedChain(ctx wrapped) {
	ctx.FlowRepair = true
}`,
			want: []string{"viaDefinedChain.FlowRepair"},
		},
		"marker assigned through a collection of defined context types": {
			dir: "model", name: "keys.go",
			source: `package model
type base actions.AgentLaunchContext

type bases []base

func viaDefinedCollection(xs bases) {
	xs[0].FlowID = "f"
}`,
			want: []string{"viaDefinedCollection.FlowID"},
		},
		"collection of an unrelated named type stays unrelated": {
			dir: "model", name: "keys.go",
			source: `package model
type thing struct{}

type things []thing

func unrelatedCollection(xs things) {
	xs[0].FlowID = "f"
}`,
			want: []string{},
		},
		"marker assigned on a promoted embedded launch context": {
			dir: "model", name: "keys.go",
			source: `package model
type wrapper struct {
	actions.AgentLaunchContext
}

func viaEmbedding(w wrapper) {
	w.FlowID = "f"
}`,
			want: []string{"viaEmbedding.FlowID"},
		},
		"marker assigned through a chain of embedded launch contexts": {
			dir: "model", name: "keys.go",
			source: `package model
type outerWrapper struct {
	wrapper
}

func viaEmbeddingChain(o outerWrapper) {
	o.FlowRepair = true
}`,
			neighbors: []flowSeamNeighbor{{source: `package model
type wrapper struct {
	actions.AgentLaunchContext
}`}},
			want: []string{"viaEmbeddingChain.FlowRepair"},
		},
		"marker assigned on the named embedded field": {
			dir: "model", name: "keys.go",
			source: `package model
type wrapper struct {
	actions.AgentLaunchContext
}

func viaEmbeddedFieldName(w wrapper) {
	w.AgentLaunchContext.FlowAgent = true
}`,
			want: []string{"viaEmbeddedFieldName.FlowAgent"},
		},
		"marker-named field on a struct embedding something else": {
			dir: "model", name: "keys.go",
			source: `package model
type plainWrapper struct {
	otherpkg.Thing
}

func unrelatedEmbedding(w plainWrapper) {
	w.FlowID = "f"
}`,
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
			neighbors: []flowSeamNeighbor{{source: `package model
type launchBatch struct {
	Contexts []actions.AgentLaunchContext
}`}},
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
	var sources []flowSeamParsedFile
	scanned := flowSeamScanFiles(t)
	if len(scanned) == 0 {
		t.Fatalf("no Go files found under %s; the seam rule is scanning nothing", flowSeamScanRoot)
	}
	for _, source := range scanned {
		file, err := parser.ParseFile(fset, source.Path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source.Path, err)
		}
		sources = append(sources, flowSeamParsedFile{dir: source.Dir, name: source.Name, file: file})
	}
	shapes := flowSeamShapesOf(sources)
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
