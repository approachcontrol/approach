package graphqlapi

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestInspectDocumentDepthLimit(t *testing.T) {
	if got := measure(t, nestedQuery(3)).depth; got != 3 {
		t.Fatalf("depth of a 3-deep query = %d, want 3", got)
	}
	if err := inspectDocument(nestedQuery(maxQueryDepth)); err != nil {
		t.Errorf("inspectDocument(depth %d) error = %v, want nil", maxQueryDepth, err)
	}
	if err := inspectDocument(nestedQuery(maxQueryDepth + 1)); !errors.Is(err, errQueryTooDeep) {
		t.Errorf("inspectDocument(depth %d) error = %v, want errQueryTooDeep", maxQueryDepth+1, err)
	}
}

func TestInspectDocumentDepthHiddenBehindFragment(t *testing.T) {
	query := "{ repos { ...Deep } }\nfragment Deep on Repo " + nestedQuery(maxQueryDepth)
	if err := inspectDocument(query); !errors.Is(err, errQueryTooDeep) {
		t.Fatalf("inspectDocument() error = %v, want errQueryTooDeep", err)
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
			go func() { done <- inspectDocument(query) }()
			select {
			case err := <-done:
				if !errors.Is(err, errFragmentCycle) {
					t.Fatalf("inspectDocument() error = %v, want errFragmentCycle", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("inspectDocument() did not return; the cycle walk is unbounded")
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
	go func() { done <- inspectDocument(query) }()
	select {
	case err := <-done:
		if !errors.Is(err, errQueryTooLarge) {
			t.Fatalf("inspectDocument() error = %v, want errQueryTooLarge", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("inspectDocument() did not return within 5s; the walk is exponential")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("inspectDocument() took %s, want bounded wall time", elapsed)
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
	if err := inspectDocument(small); err != nil {
		t.Errorf("inspectDocument(small) error = %v, want nil", err)
	}
	large := "{ ...Body }\nfragment Body on Query { " + fields(maxQueryNodes+1) + "}"
	if err := inspectDocument(large); !errors.Is(err, errQueryTooLarge) {
		t.Errorf("inspectDocument(large) error = %v, want errQueryTooLarge", err)
	}
}

func TestInspectDocumentExemptsIntrospectionFromDepthLimit(t *testing.T) {
	query := `{ __schema { types { fields { type {
		ofType { ofType { ofType { ofType { ofType { ofType { ofType { ofType { ofType {
			name
		} } } } } } } } }
	} } } } }`
	result := measure(t, query)
	if result.rawDepth <= maxQueryDepth {
		t.Fatalf("introspection fixture rawDepth = %d, want > %d (the fixture is not deep enough to prove the exemption)",
			result.rawDepth, maxQueryDepth)
	}
	if result.depth != 0 {
		t.Errorf("introspection depth = %d, want 0 (subtree exempt)", result.depth)
	}
	if err := inspectDocument(query); err != nil {
		t.Fatalf("inspectDocument() error = %v, want nil", err)
	}
}

func TestInspectDocumentRejectsNonQueryOperations(t *testing.T) {
	for _, query := range []string{
		`mutation { deleteEverything }`,
		`subscription { flows { id } }`,
		`query Ok { repos { id } } mutation Bad { nope }`,
	} {
		if err := inspectDocument(query); !errors.Is(err, errNonQueryOperation) {
			t.Errorf("inspectDocument(%q) error = %v, want errNonQueryOperation", query, err)
		}
	}
	if err := inspectDocument(`query Named { repos { id } }`); err != nil {
		t.Errorf("inspectDocument(named query) error = %v, want nil", err)
	}
}

func TestInspectDocumentIgnoresParseFailures(t *testing.T) {
	// A syntax error is a GraphQL error, not a transport error: the limiter
	// has no opinion so the request falls through to graphql.Do.
	for _, query := range []string{"{ repos { id ", "not a query at all", "{"} {
		if err := inspectDocument(query); err != nil {
			t.Errorf("inspectDocument(%q) error = %v, want nil", query, err)
		}
	}
}

func TestInspectDocumentIgnoresUndefinedFragments(t *testing.T) {
	if err := inspectDocument("{ repos { ...Missing } }"); err != nil {
		t.Fatalf("inspectDocument() error = %v, want nil (undefined fragments are a GraphQL validation error)", err)
	}
}
