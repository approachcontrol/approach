package graphqlapi

import (
	"errors"
	"strings"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
)

const (
	// maxQueryDepth bounds nested field levels. `{ repos { id } }` measures 2.
	maxQueryDepth = 12
	// maxIntrospectionDepth bounds nesting inside a __schema or __type subtree.
	// Introspection gets its own, larger limit rather than an exemption: the
	// meta-schema is recursive (__Type.fields -> __Field.type -> __Type), and
	// `fields { type { ofType { ofType {` doubles the response every four
	// levels, so an exempted subtree is an exponential blowup from a sub-1 KB
	// request. Client codegen's canonical introspection query measures 13.
	maxIntrospectionDepth = 20
	// maxIntrospectionNodes bounds field nodes inside a __schema or __type
	// subtree. Depth alone is not enough: alias fan-out across the meta-schema's
	// own lists amplifies within the depth cap, and those subtrees are exempt
	// from the cost budgets, so nothing downstream would catch it. Client
	// codegen's canonical introspection query measures 181.
	maxIntrospectionNodes = 400
	// maxQueryNodes bounds field nodes after fragment expansion. It blunts
	// alias amplification over the recursive Repo.flows <-> Flow.repo cycle.
	maxQueryNodes = 2000
	// maxWalkBudget bounds AST node visits during the limit walk itself, as a
	// backstop behind memoization.
	maxWalkBudget = 20000
	// maxQueryCost bounds the number of field values a query may resolve. It
	// bounds resolver calls and the nested maps graphql-go builds for the
	// result, both of which are per-value regardless of how wide each value
	// is.
	maxQueryCost = 500_000
	// maxResponseBytes bounds the estimated serialized response. It is a
	// separate budget because value count is a bad proxy for size: Flow
	// .instructions and Phase.notes are unbounded agent-supplied text, so a
	// query resolving a few thousand values can still serialize to hundreds of
	// megabytes — and encoding/json builds the whole body in one buffer before
	// a byte reaches the socket.
	maxResponseBytes = 16 << 20
	// fieldNameOverheadBytes covers the two quotes, colon, and separator around
	// each response key. Scalar widths already include their own quotes, since
	// fieldValueBytes measures the encoded form.
	fieldNameOverheadBytes = 4
	// typenameField resolves per object instance rather than against the
	// schema, so it is exempt from the depth limit but not the cost limit.
	typenameField = "__typename"
	// fallbackListSize is the assumed cardinality of a list field that
	// resultBounds has no entry for, and fallbackValueBytes the assumed width
	// of a scalar field it has no entry for. TestResultBoundsCoverEveryField
	// asserts the schema has neither, so these are drift insurance rather than
	// guesses on any real path — and both are deliberately large, because
	// under-counting is the one mistake that reopens the amplification hole.
	fallbackListSize   = 1000
	fallbackValueBytes = 8 << 10
)

var (
	errNonQueryOperation = errors.New("only query operations are supported")
	errQueryTooDeep      = errors.New("query exceeds the maximum nesting depth")
	errQueryTooLarge     = errors.New("query exceeds the maximum field count")
	errFragmentCycle     = errors.New("query contains a cyclic fragment spread")
	errQueryTooComplex   = errors.New("query is too complex to analyze")
	errQueryTooExpensive = errors.New("query would resolve too many values")
	errResponseTooLarge  = errors.New("query would return too large a response")
)

// documentMeasure is the result of the structural limit walk for one selection
// set.
//
// depth excludes introspection subtrees, which have their own deeper limit in
// introspectionDepth; rawDepth excludes nothing, so tests can prove an
// introspection query really is deeper than the ordinary limit it is exempt
// from. nodes is the post-expansion field count, saturated just above
// maxQueryNodes so an exponentially re-expanding (but acyclic) document
// cannot overflow.
type documentMeasure struct {
	depth              int
	rawDepth           int
	introspectionDepth int
	nodes              int
	introspectionNodes int
}

// parseQuery parses a query for the limit walks, returning (nil, nil) when the
// document does not parse.
//
// A syntactically invalid document is deliberately *not* an error: a parse
// failure is a GraphQL error, not a transport error, so the request falls
// through to graphql.Do and gets the 200-with-errors response every GraphQL
// client expects.
func parseQuery(query string) *ast.Document {
	document, err := parser.Parse(parser.ParseParams{
		Source:  query,
		Options: parser.ParseOptions{NoLocation: true, NoSource: true},
	})
	if err != nil {
		return nil
	}
	return document
}

// inspectDocument enforces every transport-level limit that can be decided
// from the document alone: operation type, nesting depth, expanded field
// count, and fragment cycles. Each failure is a 400.
//
// The cost limit is deliberately not here — it needs the snapshot, so it runs
// later, in inspectCost.
func inspectDocument(document *ast.Document) error {
	if document == nil {
		return nil
	}
	_, err := measureDocument(document)
	return err
}

// measureDocument walks the parsed document, returning the largest measure
// across its operations.
func measureDocument(document *ast.Document) (documentMeasure, error) {
	walker := &limitWalker{
		fragments: make(map[string]*ast.FragmentDefinition),
		memo:      make(map[string]documentMeasure),
		budget:    maxWalkBudget,
	}
	for _, definition := range document.Definitions {
		if fragment, ok := definition.(*ast.FragmentDefinition); ok && fragment.Name != nil {
			walker.fragments[fragment.Name.Value] = fragment
		}
	}
	// Cycles are rejected up front, over the whole spread graph. That leaves
	// an acyclic graph, which is what makes per-fragment memoization sound —
	// and a cycle must never reach the recursive walk below, because a Go
	// stack overflow is unrecoverable and would kill the whole process.
	if err := walker.rejectFragmentCycles(); err != nil {
		return documentMeasure{}, err
	}

	var worst documentMeasure
	for _, definition := range document.Definitions {
		operation, ok := definition.(*ast.OperationDefinition)
		if !ok {
			continue
		}
		if operation.Operation != ast.OperationTypeQuery {
			return documentMeasure{}, errNonQueryOperation
		}
		measure, err := walker.measureSelectionSet(operation.SelectionSet)
		if err != nil {
			return documentMeasure{}, err
		}
		worst = maxMeasure(worst, measure)
	}
	if worst.depth > maxQueryDepth || worst.introspectionDepth > maxIntrospectionDepth {
		return worst, errQueryTooDeep
	}
	if worst.nodes > maxQueryNodes || worst.introspectionNodes > maxIntrospectionNodes {
		return worst, errQueryTooLarge
	}
	return worst, nil
}

type limitWalker struct {
	fragments map[string]*ast.FragmentDefinition
	memo      map[string]documentMeasure
	budget    int
}

// rejectFragmentCycles runs a colored DFS over the fragment-spread graph.
// Spreads of undefined fragments are ignored: that is a GraphQL validation
// error, which graphql.Do reports on the 200 path.
func (w *limitWalker) rejectFragmentCycles() error {
	const (
		visiting = 1
		done     = 2
	)
	state := make(map[string]int, len(w.fragments))
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case visiting:
			return errFragmentCycle
		case done:
			return nil
		}
		fragment, ok := w.fragments[name]
		if !ok {
			return nil
		}
		state[name] = visiting
		var walk func(set *ast.SelectionSet) error
		walk = func(set *ast.SelectionSet) error {
			if set == nil {
				return nil
			}
			for _, selection := range set.Selections {
				if err := w.spend(); err != nil {
					return err
				}
				switch node := selection.(type) {
				case *ast.Field:
					if err := walk(node.SelectionSet); err != nil {
						return err
					}
				case *ast.InlineFragment:
					if err := walk(node.SelectionSet); err != nil {
						return err
					}
				case *ast.FragmentSpread:
					if node.Name == nil {
						continue
					}
					if err := visit(node.Name.Value); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := walk(fragment.SelectionSet); err != nil {
			return err
		}
		state[name] = done
		return nil
	}
	for name := range w.fragments {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func (w *limitWalker) measureSelectionSet(set *ast.SelectionSet) (documentMeasure, error) {
	if set == nil {
		return documentMeasure{}, nil
	}
	var measure documentMeasure
	for _, selection := range set.Selections {
		if err := w.spend(); err != nil {
			return documentMeasure{}, err
		}
		child, err := w.measureSelection(selection)
		if err != nil {
			return documentMeasure{}, err
		}
		measure.depth = maxInt(measure.depth, child.depth)
		measure.rawDepth = maxInt(measure.rawDepth, child.rawDepth)
		measure.introspectionDepth = maxInt(measure.introspectionDepth, child.introspectionDepth)
		measure.nodes = saturateNodes(measure.nodes + child.nodes)
		measure.introspectionNodes = saturateNodes(measure.introspectionNodes + child.introspectionNodes)
	}
	return measure, nil
}

func (w *limitWalker) measureSelection(selection ast.Selection) (documentMeasure, error) {
	switch node := selection.(type) {
	case *ast.Field:
		child, err := w.measureSelectionSet(node.SelectionSet)
		if err != nil {
			return documentMeasure{}, err
		}
		measure := documentMeasure{
			depth:              child.depth + 1,
			rawDepth:           child.rawDepth + 1,
			introspectionDepth: child.introspectionDepth,
			nodes:              saturateNodes(child.nodes + 1),
			introspectionNodes: child.introspectionNodes,
		}
		// An introspection subtree leaves the ordinary depth limit — client
		// codegen nests deeper than any data query would — and enters
		// maxIntrospectionDepth instead. It is a separate, larger limit, not an
		// exemption, because the meta-schema is recursive.
		if isIntrospectionField(node) {
			measure.depth = 0
			measure.introspectionDepth = maxInt(measure.introspectionDepth, measure.rawDepth)
		}
		// Only __schema and __type root a meta-schema subtree. __typename is a
		// leaf on an ordinary type, so counting it here would spend the
		// introspection budget on selections that cannot amplify — and it is
		// already charged, by value and by byte, on the data path.
		if isSchemaIntrospectionField(node) {
			measure.introspectionNodes = measure.nodes
		}
		return measure, nil
	case *ast.InlineFragment:
		// Inline fragments are depth-transparent: they add no nesting level.
		return w.measureSelectionSet(node.SelectionSet)
	case *ast.FragmentSpread:
		if node.Name == nil {
			return documentMeasure{}, nil
		}
		return w.measureFragment(node.Name.Value)
	default:
		return documentMeasure{}, nil
	}
}

// measureFragment memoizes each fragment's measure. Without it an acyclic
// doubling chain (F1 { ...F2 ...F2 }, F2 { ...F3 ...F3 }, ...) is a few
// hundred bytes that takes exponential time to walk; with it the walk is
// linear and the saturated node count rejects the document.
func (w *limitWalker) measureFragment(name string) (documentMeasure, error) {
	if measure, ok := w.memo[name]; ok {
		return measure, nil
	}
	fragment, ok := w.fragments[name]
	if !ok {
		return documentMeasure{}, nil
	}
	measure, err := w.measureSelectionSet(fragment.SelectionSet)
	if err != nil {
		return documentMeasure{}, err
	}
	w.memo[name] = measure
	return measure, nil
}

func (w *limitWalker) spend() error {
	if w.budget <= 0 {
		return errQueryTooComplex
	}
	w.budget--
	return nil
}

// resultBounds is the per-snapshot maximum cardinality of every list field and
// maximum width of every scalar field in the schema. It is what turns the cost
// walk from a guess into an upper bound on what a query can actually resolve
// and return.
type resultBounds struct {
	repos             int
	flows             int
	flowsPerRepo      int
	phasesPerFlow     int
	dependsOnPerPhase int
	values            fieldValueBytes
}

// listSize maps a *schema* list field to its cardinality bound; a schema field
// with no entry here is drift and gets fallbackListSize. Fields that are not
// in the schema at all never reach this — fieldShape stops at the unknown
// field, because a document containing one fails validation and never
// executes.
func (b resultBounds) listSize(parentType, field string) int {
	switch parentType + "." + field {
	case "Query.repos":
		return b.repos
	case "Query.flows":
		return b.flows
	case "Repo.flows":
		return b.flowsPerRepo
	case "Flow.phases":
		return b.phasesPerFlow
	case "Phase.dependsOn":
		return b.dependsOnPerPhase
	default:
		return fallbackListSize
	}
}

// valueBytes maps a scalar field to how wide it serializes in this snapshot.
// Object-typed fields carry no bytes of their own — their children do — so
// callers must only ask about leaves.
func (b resultBounds) valueBytes(parentType, field string) int64 {
	if width, ok := b.values[parentType+"."+field]; ok {
		return width
	}
	return fallbackValueBytes
}

// costMeasure is what a selection resolves: how many field values, and how
// many bytes those values serialize to. Both are saturated at just above their
// budget so an adversarial document cannot overflow its way back under.
type costMeasure struct {
	values int64
	bytes  int64
}

func (m costMeasure) exceedsBudget() error {
	if m.values > maxQueryCost {
		return errQueryTooExpensive
	}
	if m.bytes > maxResponseBytes {
		return errResponseTooLarge
	}
	return nil
}

func (m costMeasure) plus(other costMeasure) costMeasure {
	return costMeasure{
		values: saturate(m.values+other.values, maxQueryCost),
		bytes:  saturate(m.bytes+other.bytes, maxResponseBytes),
	}
}

// scaledBy repeats a measure once per element of the list its field resolves
// to. Callers fold in the field's own contribution first, so the whole
// occurrence — key, value, and subtree — scales together.
func (m costMeasure) scaledBy(multiplier int64) costMeasure {
	return costMeasure{
		values: scaleCost(multiplier, m.values, maxQueryCost),
		bytes:  scaleCost(multiplier, m.bytes, maxResponseBytes),
	}
}

// inspectCost rejects queries whose result would be too large *before*
// executing them.
//
// Depth and node counts are linear in the query text; the result is
// multiplicative in the data, because Repo.flows <-> Flow.repo is a cycle in
// the type graph. `{ repos { flows { repo { flows { repo { flows { id } } } } } } }`
// is 102 bytes, 11 levels deep, and 11 field nodes — inside every structural
// limit — yet resolves flows^3 values. The walk below multiplies each list
// field by its real cardinality in this snapshot and each scalar by its real
// width, so both budgets are upper bounds rather than assumed page sizes.
//
// Every operation in the document is measured, not just the one operationName
// selects. Only one executes, so that is deliberately conservative.
func inspectCost(schema *graphql.Schema, document *ast.Document, bounds resultBounds) error {
	if document == nil || schema == nil {
		return nil
	}
	walker := &costWalker{
		schema:    schema,
		bounds:    bounds,
		fragments: make(map[string]*ast.FragmentDefinition),
		memo:      make(map[string]costMeasure),
		budget:    maxWalkBudget,
	}
	for _, definition := range document.Definitions {
		if fragment, ok := definition.(*ast.FragmentDefinition); ok && fragment.Name != nil {
			walker.fragments[fragment.Name.Value] = fragment
		}
	}
	for _, definition := range document.Definitions {
		operation, ok := definition.(*ast.OperationDefinition)
		if !ok {
			continue
		}
		cost, err := walker.costOfSelectionSet(operation.SelectionSet, schema.QueryType())
		if err != nil {
			return err
		}
		if err := cost.exceedsBudget(); err != nil {
			return err
		}
	}
	return nil
}

type costWalker struct {
	schema    *graphql.Schema
	bounds    resultBounds
	fragments map[string]*ast.FragmentDefinition
	memo      map[string]costMeasure
	budget    int
}

// costOfSelectionSet sums the cost of every selection against parent, the
// object type the set is being resolved on. parent is nil once the walk leaves
// the schema (an unknown field, or a scalar's non-existent subtree); such a
// document can never execute, because an unknown field fails GraphQL
// validation for the whole request, so a flat per-node cost is safe there.
//
// This walk is only reached after measureDocument has rejected fragment
// cycles, so the recursion terminates.
func (w *costWalker) costOfSelectionSet(set *ast.SelectionSet, parent *graphql.Object) (costMeasure, error) {
	if set == nil {
		return costMeasure{}, nil
	}
	var total costMeasure
	for _, selection := range set.Selections {
		if err := w.spend(); err != nil {
			return costMeasure{}, err
		}
		cost, err := w.costOfSelection(selection, parent)
		if err != nil {
			return costMeasure{}, err
		}
		total = total.plus(cost)
	}
	return total, nil
}

func (w *costWalker) costOfSelection(selection ast.Selection, parent *graphql.Object) (costMeasure, error) {
	switch node := selection.(type) {
	case *ast.Field:
		// __schema and __type resolve against the schema, which is small and
		// fixed, not against the snapshot, so they are exempt. __typename is
		// NOT: it resolves once per object instance, so under a list traversal
		// it puts snapshot-proportional bytes on the wire like any other leaf.
		if isSchemaIntrospectionField(node) {
			return costMeasure{}, nil
		}
		multiplier, child := w.fieldShape(node, parent)
		cost, err := w.costOfSelectionSet(node.SelectionSet, child)
		if err != nil {
			return costMeasure{}, err
		}
		return cost.plus(w.ownCost(node, parent, child != nil)).scaledBy(multiplier), nil
	case *ast.InlineFragment:
		// The schema has no interfaces or unions, so an inline fragment's type
		// condition is always the enclosing type; anything else is a
		// validation error graphql.Do rejects before execution.
		return w.costOfSelectionSet(node.SelectionSet, parent)
	case *ast.FragmentSpread:
		if node.Name == nil {
			return costMeasure{}, nil
		}
		return w.costOfFragment(node.Name.Value, parent)
	default:
		return costMeasure{}, nil
	}
}

// ownCost is what one occurrence of the field contributes before its subtree:
// one resolved value, plus the bytes it serializes to. Object-typed fields
// carry only the key and braces — their scalar leaves carry the payload.
func (w *costWalker) ownCost(node *ast.Field, parent *graphql.Object, isObject bool) costMeasure {
	// The response key is the alias when there is one, and it is client-chosen
	// and unbounded. Charging the field name instead would leave a 60 KiB
	// alias under a list traversal free.
	bytes := int64(fieldNameOverheadBytes + len(responseKey(node)))
	if isObject || parent == nil || node.Name == nil {
		return costMeasure{values: 1, bytes: bytes}
	}
	if node.Name.Value == typenameField {
		// __typename resolves to the enclosing type's name.
		bytes += int64(len(parent.Name())) + 2
	} else {
		bytes += w.bounds.valueBytes(parent.Name(), node.Name.Value)
	}
	return costMeasure{values: 1, bytes: bytes}
}

// responseKey is the key graphql-go writes for this field: the alias when one
// is present, otherwise the field name.
func responseKey(node *ast.Field) string {
	if node.Alias != nil && node.Alias.Value != "" {
		return node.Alias.Value
	}
	if node.Name != nil {
		return node.Name.Value
	}
	return ""
}

// fieldShape returns how many times the field's subtree is resolved and the
// object type its subtree resolves against. A nil object means the walk has
// left the schema — an unknown field, or a scalar's non-existent subtree.
func (w *costWalker) fieldShape(node *ast.Field, parent *graphql.Object) (int64, *graphql.Object) {
	if parent == nil || node.Name == nil {
		return 1, nil
	}
	definition, ok := parent.Fields()[node.Name.Value]
	if !ok || definition == nil {
		return 1, nil
	}
	multiplier := int64(1)
	named := definition.Type
	for {
		switch typed := named.(type) {
		case *graphql.NonNull:
			named = typed.OfType
			continue
		case *graphql.List:
			multiplier = saturate(multiplier*int64(w.bounds.listSize(parent.Name(), node.Name.Value)), maxQueryCost)
			named = typed.OfType
			continue
		}
		break
	}
	object, _ := named.(*graphql.Object)
	return multiplier, object
}

// costOfFragment memoizes per (fragment, parent type) — the same reason
// measureFragment memoizes, and keyed by parent because a fragment's cost
// depends on the type it is spread into.
func (w *costWalker) costOfFragment(name string, parent *graphql.Object) (costMeasure, error) {
	key := name + "\x00" + objectName(parent)
	if cost, ok := w.memo[key]; ok {
		return cost, nil
	}
	fragment, ok := w.fragments[name]
	if !ok {
		return costMeasure{}, nil
	}
	cost, err := w.costOfSelectionSet(fragment.SelectionSet, parent)
	if err != nil {
		return costMeasure{}, err
	}
	w.memo[key] = cost
	return cost, nil
}

func (w *costWalker) spend() error {
	if w.budget <= 0 {
		return errQueryTooComplex
	}
	w.budget--
	return nil
}

// scaleCost is `multiplier * cost`, saturated at just above budget. The
// early return keeps the product itself from overflowing: both operands are
// already clamped to budget, but their product need not be.
func scaleCost(multiplier, cost, budget int64) int64 {
	if multiplier > budget || cost > budget {
		return budget + 1
	}
	return saturate(multiplier*cost, budget)
}

// isIntrospectionField gates which depth limit applies. It covers __typename
// harmlessly: a leaf adds no nesting under either limit.
func isIntrospectionField(node *ast.Field) bool {
	return node.Name != nil && strings.HasPrefix(node.Name.Value, "__")
}

// isSchemaIntrospectionField gates the *cost* exemption, which must not cover
// __typename — that one resolves per snapshot object, not against the schema.
func isSchemaIntrospectionField(node *ast.Field) bool {
	return isIntrospectionField(node) && node.Name.Value != typenameField
}

func objectName(object *graphql.Object) string {
	if object == nil {
		return ""
	}
	return object.Name()
}

func maxMeasure(left, right documentMeasure) documentMeasure {
	return documentMeasure{
		depth:              maxInt(left.depth, right.depth),
		rawDepth:           maxInt(left.rawDepth, right.rawDepth),
		introspectionDepth: maxInt(left.introspectionDepth, right.introspectionDepth),
		nodes:              saturateNodes(maxInt(left.nodes, right.nodes)),
		introspectionNodes: saturateNodes(maxInt(left.introspectionNodes, right.introspectionNodes)),
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func saturateNodes(count int) int {
	if count > maxQueryNodes {
		return maxQueryNodes + 1
	}
	return count
}

func saturate(value, budget int64) int64 {
	if value > budget {
		return budget + 1
	}
	return value
}
