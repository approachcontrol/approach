package graphqlapi

import (
	"errors"
	"strings"

	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
)

const (
	// maxQueryDepth bounds nested field levels. `{ repos { id } }` measures 2.
	maxQueryDepth = 12
	// maxQueryNodes bounds field nodes after fragment expansion. It blunts
	// alias amplification over the recursive Repo.flows <-> Flow.repo cycle.
	maxQueryNodes = 2000
	// maxWalkBudget bounds AST node visits during the limit walk itself, as a
	// backstop behind memoization.
	maxWalkBudget = 20000
)

var (
	errNonQueryOperation = errors.New("only query operations are supported")
	errQueryTooDeep      = errors.New("query exceeds the maximum nesting depth")
	errQueryTooLarge     = errors.New("query exceeds the maximum field count")
	errFragmentCycle     = errors.New("query contains a cyclic fragment spread")
	errQueryTooComplex   = errors.New("query is too complex to analyze")
)

// documentMeasure is the result of the limit walk for one selection set.
//
// depth applies the introspection exemption; rawDepth does not, so tests can
// prove an introspection query really is deeper than the limit it is exempt
// from. nodes is the post-expansion field count, saturated just above
// maxQueryNodes so an exponentially re-expanding (but acyclic) document
// cannot overflow.
type documentMeasure struct {
	depth    int
	rawDepth int
	nodes    int
}

// inspectDocument enforces every transport-level limit that requires a parsed
// document: operation type, nesting depth, expanded field count, and fragment
// cycles. Each failure is a 400.
//
// A syntactically invalid document is deliberately *not* an error here: a
// parse failure is a GraphQL error, not a transport error, so the request
// falls through to graphql.Do and gets the 200-with-errors response every
// GraphQL client expects.
func inspectDocument(query string) error {
	document, err := parser.Parse(parser.ParseParams{
		Source:  query,
		Options: parser.ParseOptions{NoLocation: true, NoSource: true},
	})
	if err != nil {
		return nil
	}
	_, err = measureDocument(document)
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
	if worst.depth > maxQueryDepth {
		return worst, errQueryTooDeep
	}
	if worst.nodes > maxQueryNodes {
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
		measure.nodes = saturateNodes(measure.nodes + child.nodes)
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
			depth:    child.depth + 1,
			rawDepth: child.rawDepth + 1,
			nodes:    saturateNodes(child.nodes + 1),
		}
		// Introspection subtrees are exempt from the depth limit so client
		// codegen keeps working. They still count toward the node cap, which
		// is what bounds __type(name:) recursion.
		if node.Name != nil && strings.HasPrefix(node.Name.Value, "__") {
			measure.depth = 0
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

func maxMeasure(left, right documentMeasure) documentMeasure {
	return documentMeasure{
		depth:    maxInt(left.depth, right.depth),
		rawDepth: maxInt(left.rawDepth, right.rawDepth),
		nodes:    saturateNodes(maxInt(left.nodes, right.nodes)),
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
