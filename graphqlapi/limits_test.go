package graphqlapi

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/parser"
)

func nestedQuery(depth int) string {
	inner := "a"
	for level := 1; level < depth; level++ {
		inner = "a { " + inner + " }"
	}
	return "{ " + inner + " }"
}

func measure(t *testing.T, query string) documentMeasure {
	t.Helper()
	document, err := parser.Parse(parser.ParseParams{Source: query})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	result, err := measureDocument(document)
	if err != nil {
		t.Fatalf("measureDocument() error = %v", err)
	}
	return result
}

// inspect runs the document-only limit walk, which is the half of the limits
// that needs no snapshot. Cost is exercised separately, against real bounds.
func inspect(query string) error {
	return inspectDocument(parseQuery(query))
}

func TestInspectDocumentDepthLimit(t *testing.T) {
	if got := measure(t, nestedQuery(3)).depth; got != 3 {
		t.Fatalf("depth of a 3-deep query = %d, want 3", got)
	}
	if err := inspect(nestedQuery(maxQueryDepth)); err != nil {
		t.Errorf("inspect(depth %d) error = %v, want nil", maxQueryDepth, err)
	}
	if err := inspect(nestedQuery(maxQueryDepth + 1)); !errors.Is(err, errQueryTooDeep) {
		t.Errorf("inspect(depth %d) error = %v, want errQueryTooDeep", maxQueryDepth+1, err)
	}
}

func TestInspectDocumentDepthHiddenBehindFragment(t *testing.T) {
	query := "{ repos { ...Deep } }\nfragment Deep on Repo " + nestedQuery(maxQueryDepth)
	if err := inspect(query); !errors.Is(err, errQueryTooDeep) {
		t.Fatalf("inspect() error = %v, want errQueryTooDeep", err)
	}
}

func TestInspectDocumentInlineFragmentIsDepthTransparent(t *testing.T) {
	plain := measure(t, "{ repos { flows { id } } }")
	inlined := measure(t, "{ repos { ... on Repo { flows { ... on Flow { id } } } } }")
	if plain.depth != inlined.depth {
		t.Fatalf("inline fragment depth = %d, want %d", inlined.depth, plain.depth)
	}
}

func TestInspectDocumentRejectsFragmentCycles(t *testing.T) {
	cases := map[string]string{
		"mutual": `{ repos { ...A } }
			fragment A on Repo { flows { ...B } }
			fragment B on Flow { repo { ...A } }`,
		"self": `{ repos { ...A } }
			fragment A on Repo { ...A }`,
		"three-hop": `{ repos { ...A } }
			fragment A on Repo { flows { ...B } }
			fragment B on Flow { repo { ...C } }
			fragment C on Repo { flows { ...B } }`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- inspect(query) }()
			select {
			case err := <-done:
				if !errors.Is(err, errFragmentCycle) {
					t.Fatalf("inspect() error = %v, want errFragmentCycle", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("inspect() did not return; the cycle walk is unbounded")
			}
		})
	}
}

func TestInspectDocumentRejectsAcyclicDoublingChainQuickly(t *testing.T) {
	// F0 { ...F1 ...F1 }, F1 { ...F2 ...F2 }, ... is a few hundred bytes but
	// expands to 2^39 fields. Memoization must keep the walk linear.
	const chain = 40
	var builder strings.Builder
	builder.WriteString("{ ...F0 }\n")
	for level := 0; level < chain-1; level++ {
		fmt.Fprintf(&builder, "fragment F%d on Query { ...F%d ...F%d }\n", level, level+1, level+1)
	}
	fmt.Fprintf(&builder, "fragment F%d on Query { leaf }\n", chain-1)
	query := builder.String()
	if len(query) > MaxRequestBytes {
		t.Fatalf("fixture is %d bytes, which the body cap would have rejected first", len(query))
	}

	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- inspect(query) }()
	select {
	case err := <-done:
		if !errors.Is(err, errQueryTooLarge) {
			t.Fatalf("inspect() error = %v, want errQueryTooLarge", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("inspect() did not return within 5s; the walk is exponential")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("inspect() took %s, want bounded wall time", elapsed)
	}
}

func TestInspectDocumentNodeCapCountsFragmentBodies(t *testing.T) {
	fields := func(count int) string {
		var builder strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&builder, "f%d ", i)
		}
		return builder.String()
	}
	small := "{ ...Body }\nfragment Body on Query { " + fields(100) + "}"
	if err := inspect(small); err != nil {
		t.Errorf("inspect(small) error = %v, want nil", err)
	}
	large := "{ ...Body }\nfragment Body on Query { " + fields(maxQueryNodes+1) + "}"
	if err := inspect(large); !errors.Is(err, errQueryTooLarge) {
		t.Errorf("inspect(large) error = %v, want errQueryTooLarge", err)
	}
}

func TestInspectDocumentTakesIntrospectionOutOfTheOrdinaryDepthLimit(t *testing.T) {
	query := `{ __schema { types { fields { type {
		ofType { ofType { ofType { ofType { ofType { ofType { ofType { ofType { ofType {
			name
		} } } } } } } } }
	} } } } }`
	result := measure(t, query)
	if result.rawDepth <= maxQueryDepth {
		t.Fatalf("introspection fixture rawDepth = %d, want > %d (the fixture is not deep enough to prove anything)",
			result.rawDepth, maxQueryDepth)
	}
	if result.depth != 0 {
		t.Errorf("introspection depth = %d, want 0 (it leaves the ordinary limit)", result.depth)
	}
	if result.introspectionDepth != result.rawDepth {
		t.Errorf("introspectionDepth = %d, want %d (it enters the introspection limit instead)",
			result.introspectionDepth, result.rawDepth)
	}
	if err := inspect(query); err != nil {
		t.Fatalf("inspect() error = %v, want nil", err)
	}
}

func TestInspectDocumentRejectsNonQueryOperations(t *testing.T) {
	for _, query := range []string{
		`mutation { deleteEverything }`,
		`subscription { flows { id } }`,
		`query Ok { repos { id } } mutation Bad { nope }`,
	} {
		if err := inspect(query); !errors.Is(err, errNonQueryOperation) {
			t.Errorf("inspect(%q) error = %v, want errNonQueryOperation", query, err)
		}
	}
	if err := inspect(`query Named { repos { id } }`); err != nil {
		t.Errorf("inspect(named query) error = %v, want nil", err)
	}
}

func TestInspectDocumentIgnoresParseFailures(t *testing.T) {
	// A syntax error is a GraphQL error, not a transport error: the limiter
	// has no opinion so the request falls through to graphql.Do.
	for _, query := range []string{"{ repos { id ", "not a query at all", "{"} {
		if err := inspect(query); err != nil {
			t.Errorf("inspect(%q) error = %v, want nil", query, err)
		}
	}
}

func TestInspectDocumentIgnoresUndefinedFragments(t *testing.T) {
	if err := inspect("{ repos { ...Missing } }"); err != nil {
		t.Fatalf("inspect() error = %v, want nil (undefined fragments are a GraphQL validation error)", err)
	}
}

func TestInspectDocumentWalkBudgetRejectsUltraDeepDocument(t *testing.T) {
	// The recursion in measureDocument is not tail-recursive and a Go stack
	// overflow is unrecoverable, so the walk budget is the last thing standing
	// between an adversarial document and a dead process. `a{` plus `}` is
	// three bytes per level, so a body-cap-sized document nests far past the
	// budget — and this must be caught without exhausting the stack.
	levels := maxWalkBudget + 1
	query := "{" + strings.Repeat("a{", levels) + "x" + strings.Repeat("}", levels) + "}"
	if len(query) > MaxRequestBytes {
		t.Fatalf("fixture is %d bytes, which the body cap would have rejected first", len(query))
	}
	if parseQuery(query) == nil {
		t.Fatalf("fixture does not parse, so it proves nothing about the walk budget")
	}

	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- inspect(query) }()
	select {
	case err := <-done:
		if !errors.Is(err, errQueryTooComplex) {
			t.Fatalf("inspect(ultra-deep) error = %v, want errQueryTooComplex", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("inspect() did not return within 10s")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("inspect() took %s, want bounded wall time", elapsed)
	}
}

// costOf measures a query against the given cardinalities, filling in field
// widths from the ordinary test fixture so these cases exercise cardinality
// rather than the deliberately pessimistic fallbackValueBytes. Byte-budget
// behavior is covered separately, with real widths.
func costOf(t *testing.T, query string, bounds resultBounds) error {
	t.Helper()
	if bounds.values == nil {
		repos, flows := testSources()
		bounds.values = buildSnapshot(repos, flows, nil).bounds().values
	}
	schema, err := newSchema()
	if err != nil {
		t.Fatalf("newSchema() error = %v", err)
	}
	document := parseQuery(query)
	if document == nil {
		t.Fatalf("parseQuery(%q) did not parse", query)
	}
	return inspectCost(&schema, document, bounds)
}

// cycleQuery nests repos { flows { repo { flows { ... } } } }, the shape that
// makes the type graph's Repo.flows <-> Flow.repo cycle multiplicative in the
// data while staying trivially small in the query text.
func cycleQuery(levels int) string {
	inner := "id"
	for level := 0; level < levels; level++ {
		inner = "flows { " + inner + " }"
		if level < levels-1 {
			inner = "repo { " + inner + " }"
		}
	}
	return "{ repos { " + inner + " } }"
}

func TestInspectCostRejectsResultAmplification(t *testing.T) {
	// One repo with thirty flows: every structural limit passes with room to
	// spare, and the response is hundreds of megabytes.
	bounds := resultBounds{repos: 1, flows: 30, flowsPerRepo: 30, phasesPerFlow: 8}
	query := cycleQuery(5)

	if len(query) > 200 {
		t.Fatalf("fixture is %d bytes; the point is that it is tiny", len(query))
	}
	measured := measure(t, query)
	if measured.depth > maxQueryDepth || measured.nodes > maxQueryNodes {
		t.Fatalf("fixture depth=%d nodes=%d already trips a structural limit; it no longer proves cost is load-bearing",
			measured.depth, measured.nodes)
	}
	if err := costOf(t, query, bounds); !errors.Is(err, errQueryTooExpensive) {
		t.Fatalf("inspectCost(5-level cycle) error = %v, want errQueryTooExpensive", err)
	}
}

func TestInspectCostAllowsOrdinaryQueries(t *testing.T) {
	bounds := resultBounds{repos: 40, flows: 400, flowsPerRepo: 60, phasesPerFlow: 20, dependsOnPerPhase: 4}
	for name, query := range map[string]string{
		"repo list":         `{ repos { id path displayName isBare inScanRoot } }`,
		"flow list":         `{ flows { id title status repoPath updatedAt } }`,
		"dashboard":         `{ flows { id title status currentPhase { id title status } phases { id title kind status order dependsOn } repo { id displayName } } }`,
		"by repo":           `{ repos { id displayName flows { id title status phases { id status } } } }`,
		"one flow":          `{ flow(id: "f1") { id title phases { id title kind status dependsOn } repo { id flows { id } } } }`,
		"one shallow cycle": cycleQuery(2),
	} {
		t.Run(name, func(t *testing.T) {
			if err := costOf(t, query, bounds); err != nil {
				t.Fatalf("inspectCost() error = %v, want nil", err)
			}
		})
	}
}

func TestInspectCostScalesWithSnapshotSize(t *testing.T) {
	// The same query is fine against a small snapshot and rejected against a
	// large one. That is the whole point of bounding with real cardinality
	// rather than an assumed page size.
	query := cycleQuery(3)
	small := resultBounds{repos: 3, flows: 6, flowsPerRepo: 4}
	large := resultBounds{repos: 500, flows: 5000, flowsPerRepo: 400}

	if err := costOf(t, query, small); err != nil {
		t.Errorf("inspectCost(small snapshot) error = %v, want nil", err)
	}
	if err := costOf(t, query, large); !errors.Is(err, errQueryTooExpensive) {
		t.Errorf("inspectCost(large snapshot) error = %v, want errQueryTooExpensive", err)
	}
}

func TestInspectCostCountsAliasesSeparately(t *testing.T) {
	// Aliases are the cheap way to widen a level without nesting it, so each
	// alias has to cost its own subtree.
	bounds := resultBounds{repos: 50, flows: 500, flowsPerRepo: 200}
	var builder strings.Builder
	builder.WriteString("{ repos { ")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&builder, "a%d: flows { repo { id } } ", i)
	}
	builder.WriteString("} }")
	if err := costOf(t, builder.String(), bounds); !errors.Is(err, errQueryTooExpensive) {
		t.Fatalf("inspectCost(alias-widened) error = %v, want errQueryTooExpensive", err)
	}
}

func TestInspectCostChargesKeysOfEmptyLists(t *testing.T) {
	// A field's response key is written once per parent object, whatever its
	// list resolves to, so an empty list must not zero out the key. Charging
	// the key inside the multiplier made `{ repos { <big alias>: flows { id } } }`
	// free on any snapshot with no Flows.
	bounds := resultBounds{repos: 400, flows: 0, flowsPerRepo: 0, values: fieldValueBytes{}}
	alias := strings.Repeat("a", 55<<10)
	if err := costOf(t, "{ repos { "+alias+": flows { id } } }", bounds); !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("inspectCost(aliased empty list) error = %v, want errResponseTooLarge", err)
	}
	// It is the alias width that costs, not the empty list itself: an ordinary
	// key over the same shape stays well inside the budget.
	if err := costOf(t, "{ repos { x: flows { id } } }", bounds); err != nil {
		t.Fatalf("inspectCost(short key, empty list) error = %v, want nil", err)
	}
}

func TestInspectCostExemptsIntrospection(t *testing.T) {
	// Introspection resolves against the schema, which is small and fixed, so
	// snapshot size must not make client codegen start failing.
	query := `{ __schema { types { name kind fields { name args { name type { name ofType { name } } } } } } }`
	huge := resultBounds{repos: 100000, flows: 100000, flowsPerRepo: 100000, phasesPerFlow: 1000, dependsOnPerPhase: 100}
	if err := costOf(t, query, huge); err != nil {
		t.Fatalf("inspectCost(introspection) error = %v, want nil", err)
	}
}

func TestInspectCostIgnoresUnknownFields(t *testing.T) {
	// An unknown field fails GraphQL validation for the whole document, so
	// nothing executes and there is no result to bound. Charging it the
	// fallback list size would turn every typo into a confusing 400.
	bounds := resultBounds{repos: 1000, flows: 1000, flowsPerRepo: 1000}
	if err := costOf(t, `{ nope { alsoNope { stillNope } } }`, bounds); err != nil {
		t.Fatalf("inspectCost(unknown fields) error = %v, want nil", err)
	}
}

// TestResultBoundsCoverEveryField is the drift guard for resultBounds. A new
// list field with no cardinality bound, or a new scalar field with no measured
// width, silently falls back to a guess — exactly what this design removed.
// Both fallbacks are pessimistic, so drift shows up as spurious 400s rather
// than as a hole; this test turns that into a build failure instead.
func TestResultBoundsCoverEveryField(t *testing.T) {
	schema, err := newSchema()
	if err != nil {
		t.Fatalf("newSchema() error = %v", err)
	}
	// Cardinalities distinct and non-zero, so a field wired to the wrong bound
	// still shows up as an unmapped fallback rather than a silent zero. Widths
	// come from a fixture that populates every optional field.
	bounds := resultBounds{repos: 2, flows: 3, flowsPerRepo: 5, phasesPerFlow: 7, dependsOnPerPhase: 11}
	bounds.values = buildSnapshot(fullyPopulatedSources()).bounds().values

	for typeName, definition := range schema.TypeMap() {
		object, ok := definition.(*graphql.Object)
		if !ok || strings.HasPrefix(typeName, "__") {
			continue
		}
		for fieldName, field := range object.Fields() {
			if isListType(field.Type) {
				if got := bounds.listSize(typeName, fieldName); got == fallbackListSize {
					t.Errorf("%s.%s is a list field with no resultBounds entry; add one in resultBounds.listSize and snapshot.bounds",
						typeName, fieldName)
				}
			}
			if !isLeafType(field.Type) {
				continue
			}
			if _, measured := bounds.values[typeName+"."+fieldName]; !measured {
				t.Errorf("%s.%s is a scalar field with no measured width; observe it in fieldValueBytes", typeName, fieldName)
			}
		}
	}
}

func isListType(candidate graphql.Type) bool {
	for {
		switch typed := candidate.(type) {
		case *graphql.NonNull:
			candidate = typed.OfType
		case *graphql.List:
			return true
		default:
			return false
		}
	}
}

// isLeafType reports whether a field carries bytes of its own rather than
// delegating them to a child selection set.
func isLeafType(candidate graphql.Type) bool {
	for {
		switch typed := candidate.(type) {
		case *graphql.NonNull:
			candidate = typed.OfType
		case *graphql.List:
			candidate = typed.OfType
		case *graphql.Object:
			return false
		default:
			return true
		}
	}
}

// introspectionCycleQuery repeats the meta-schema's own recursion:
// __Type.fields -> __Field.type -> __Type. Each repetition adds four levels
// and doubles the response, so an *exempt* introspection subtree is an
// exponential blowup from a sub-1 KB request.
func introspectionCycleQuery(repetitions int) string {
	inner := "name"
	for i := 0; i < repetitions; i++ {
		inner = "fields { type { ofType { ofType { " + inner + " } } } }"
	}
	return "{ __schema { types { " + inner + " } } }"
}

func TestInspectDocumentBoundsIntrospectionDepth(t *testing.T) {
	// Well inside the node cap, and the ordinary depth limit never sees it.
	deep := introspectionCycleQuery(10)
	measured, _ := measureDocument(parseQuery(deep))
	if measured.depth != 0 {
		t.Fatalf("introspection depth = %d, want 0 (it leaves the ordinary limit)", measured.depth)
	}
	if measured.nodes > maxQueryNodes {
		t.Fatalf("fixture nodes = %d, over the node cap; it no longer proves the depth cap is load-bearing", measured.nodes)
	}
	if len(deep) > 1000 {
		t.Fatalf("fixture is %d bytes; the point is that it is tiny", len(deep))
	}
	if err := inspect(deep); !errors.Is(err, errQueryTooDeep) {
		t.Fatalf("inspect(deep introspection) error = %v, want errQueryTooDeep", err)
	}
	if err := inspect(`{ __type(name: "__Type") { ` +
		strings.Repeat("fields { type { ofType { ofType { ", 10) + "name" +
		strings.Repeat(" } } } }", 10) + " } }"); !errors.Is(err, errQueryTooDeep) {
		t.Fatal("__type(name:) recursion is not bounded")
	}
}

func TestInspectDocumentAllowsCodegenIntrospection(t *testing.T) {
	// The canonical introspection query every GraphQL client generates. It is
	// deeper than maxQueryDepth, which is the whole reason introspection has
	// its own limit, and it must keep working.
	measured := measure(t, codegenIntrospectionQuery)
	if measured.rawDepth <= maxQueryDepth {
		t.Fatalf("fixture rawDepth = %d, want > %d (it would not prove the exemption)", measured.rawDepth, maxQueryDepth)
	}
	if measured.introspectionDepth > maxIntrospectionDepth {
		t.Fatalf("canonical introspection measures depth %d, over the %d cap — real client codegen would break",
			measured.introspectionDepth, maxIntrospectionDepth)
	}
	if measured.introspectionNodes > maxIntrospectionNodes {
		t.Fatalf("canonical introspection measures %d nodes, over the %d cap — real client codegen would break",
			measured.introspectionNodes, maxIntrospectionNodes)
	}
	if err := inspect(codegenIntrospectionQuery); err != nil {
		t.Fatalf("inspect(codegen introspection) error = %v, want nil", err)
	}
}

// codegenIntrospectionQuery is the standard graphql-js IntrospectionQuery.
const codegenIntrospectionQuery = `query IntrospectionQuery {
  __schema {
    queryType { name } mutationType { name } subscriptionType { name }
    types { ...FullType }
    directives { name description locations args { ...InputValue } }
  }
}
fragment FullType on __Type {
  kind name description
  fields(includeDeprecated: true) {
    name description args { ...InputValue } type { ...TypeRef } isDeprecated deprecationReason
  }
  inputFields { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
  possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue { name description type { ...TypeRef } defaultValue }
fragment TypeRef on __Type {
  kind name
  ofType { kind name ofType { kind name ofType { kind name
    ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } }
}`

func TestInspectDocumentBoundsIntrospectionNodes(t *testing.T) {
	// Depth alone does not bound introspection. Alias fan-out across the
	// meta-schema's own lists amplifies freely *within* the depth cap, and
	// __schema is exempt from the cost budgets, so nothing downstream would
	// catch it: this shape returned 23 MiB — past the documented response
	// ceiling — from a 34 KiB request.
	var aliases strings.Builder
	for i := 0; i < maxIntrospectionNodes*4; i++ {
		fmt.Fprintf(&aliases, "a%d: description ", i)
	}
	query := "{ __schema { types { fields { type { ofType { ofType { " + aliases.String() + " } } } } } } }"
	if len(queryBody(t, query)) > MaxRequestBytes {
		t.Fatalf("fixture is %d bytes, which the body cap would have rejected first", len(query))
	}
	measured, _ := measureDocument(parseQuery(query))
	if measured.introspectionDepth > maxIntrospectionDepth {
		t.Fatalf("fixture introspectionDepth = %d, over the depth cap; it no longer proves the node cap is load-bearing",
			measured.introspectionDepth)
	}
	if measured.nodes > maxQueryNodes {
		t.Fatalf("fixture nodes = %d, over the ordinary node cap; it no longer proves the introspection cap is load-bearing",
			measured.nodes)
	}
	if err := inspect(query); !errors.Is(err, errQueryTooLarge) {
		t.Fatalf("inspect(alias-fanned introspection) error = %v, want errQueryTooLarge", err)
	}
}
