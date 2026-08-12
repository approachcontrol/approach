package graphqlapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
)

// The Next.js viewer in web/ hard-codes its GraphQL documents as TypeScript
// template literals. Nothing else binds them to this schema: rename a field or
// tighten a nullability here and both CI jobs stay green while the deployed app
// breaks. These tests parse the documents straight out of the client source and
// hold them to the same validation and limit walk the server applies, so schema
// drift fails in this package rather than in production.
const webClientSource = "../web/src/lib/approach-api.ts"

// Matches a "const NAME_QUERY =" bound to a template literal, the only shape
// the client uses. The literals contain no interpolation, so the document text
// is exactly what reaches the server.
var webQueryPattern = regexp.MustCompile("(?s)const ([A-Z_]+_QUERY) = `(.*?)`")

// webClientQueries returns the documents by constant name. It fails rather than
// skipping when the source or a document is missing: an extraction that
// silently finds nothing would make every assertion below vacuous.
func webClientQueries(t *testing.T) map[string]string {
	t.Helper()
	source, err := os.ReadFile(filepath.Clean(webClientSource))
	if err != nil {
		t.Fatalf("read %s: %v (the web client is the subject of this test; update or remove it together)", webClientSource, err)
	}

	matches := webQueryPattern.FindAllStringSubmatch(string(source), -1)
	queries := make(map[string]string, len(matches))
	for _, match := range matches {
		queries[match[1]] = match[2]
	}

	for _, name := range []string{"REPOS_QUERY", "REPO_QUERY", "FLOW_QUERY"} {
		if strings.TrimSpace(queries[name]) == "" {
			t.Fatalf("no %s found in %s; the extraction pattern or the client changed shape", name, webClientSource)
		}
	}

	// A document declared in a shape the pattern misses would be silently
	// unchecked, and naming the three known ones above would not catch it. Count
	// declarations independently and require that every one was extracted.
	declared := strings.Count(string(source), "_QUERY = ")
	if declared != len(queries) {
		t.Fatalf("%s declares %d *_QUERY constants but only %d matched the extraction pattern; a document is going unchecked",
			webClientSource, declared, len(queries))
	}
	return queries
}

// mustParse guards the assertions below. parseQuery returns nil for an
// unparseable document — deliberate in production, where a syntax error is a
// GraphQL error rather than a transport one — and inspectDocument(nil) is a
// no-op, so an unguarded parse would make the limit test vacuous.
func mustParse(t *testing.T, name, query string) *ast.Document {
	t.Helper()
	document := parseQuery(query)
	if document == nil {
		t.Fatalf("%s does not parse as a GraphQL document", name)
	}
	return document
}

// TestWebClientDocumentsValidateAgainstSchema is the drift guard: every field,
// argument, and variable type the client selects must exist here.
func TestWebClientDocumentsValidateAgainstSchema(t *testing.T) {
	schema, err := newSchema()
	if err != nil {
		t.Fatalf("newSchema() error = %v", err)
	}

	for name, query := range webClientQueries(t) {
		t.Run(name, func(t *testing.T) {
			result := graphql.ValidateDocument(&schema, mustParse(t, name, query), nil)
			if !result.IsValid {
				for _, validationErr := range result.Errors {
					t.Errorf("%s is not valid against the schema: %s", name, validationErr.Message)
				}
			}
		})
	}
}

// TestWebClientDocumentsWithinLimits keeps the client's documents inside the
// document-only half of the limiter. A document that validates but exceeds
// depth or node count would be rejected at request time with a 400.
func TestWebClientDocumentsWithinLimits(t *testing.T) {
	for name, query := range webClientQueries(t) {
		t.Run(name, func(t *testing.T) {
			if err := inspectDocument(mustParse(t, name, query)); err != nil {
				t.Errorf("%s exceeds a request limit: %v", name, err)
			}
		})
	}
}

// TestWebClientDocumentsOmitFreeText mirrors the client's own guard. A deployed
// instance republishes machine-local state, so the unbounded agent-authored
// fields stay unselected; asserting it here means adding them to the client is
// a Go test failure too, not only a vitest one.
func TestWebClientDocumentsOmitFreeText(t *testing.T) {
	for name, query := range webClientQueries(t) {
		for _, field := range []string{"instructions", "notes"} {
			if strings.Contains(query, field) {
				t.Errorf("%s selects %q; the web viewer deliberately never renders unbounded agent-authored text", name, field)
			}
		}
	}
}
