package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/internal/launchcontrol"
)

// Every `approach flow` leaf must be classified by the launch control verb
// table. A new leaf that is not in this map fails here rather than defaulting
// to "unproxied" and silently opening the database from inside an agent.
func TestVerbTableCoversEveryFlowCLILeaf(t *testing.T) {
	leafVerbs := map[string]launchcontrol.Verb{
		"runFlow/create":               launchcontrol.VerbFlowCreate,
		"runFlow/list":                 launchcontrol.VerbFlowList,
		"runFlow/read":                 launchcontrol.VerbFlowRead,
		"runFlow/plan":                 launchcontrol.VerbPlanSet,
		"runFlow/issue":                launchcontrol.VerbIssueSet,
		"runFlow/pr":                   launchcontrol.VerbPRSet,
		"runFlow/merge":                launchcontrol.VerbMergeSet,
		"runFlowPhase/set":             launchcontrol.VerbPhaseSet,
		"runFlowPhase/complete":        launchcontrol.VerbPhaseComplete,
		"runFlowPhase/block":           launchcontrol.VerbPhaseBlock,
		"runFlowPhase/needs-attention": launchcontrol.VerbPhaseNeedsAttention,
		"runFlowPhase/restart":         launchcontrol.VerbPhaseRestart,
		"runFlowPhase/reset":           launchcontrol.VerbPhaseReset,
		"runFlowPhase/recover":         launchcontrol.VerbPhaseRecover,
		"runFlowPhase/add-child":       launchcontrol.VerbPhaseAddChild,
		"runFlowPhase/agent":           launchcontrol.VerbPhaseAgentSet,
	}
	// runFlow/phase is a router, not a leaf.
	routers := map[string]bool{"runFlow/phase": true}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "flow.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "runFlow" && fn.Name.Name != "runFlowPhase") {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				leaf, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				key := fn.Name.Name + "/" + leaf
				seen[key] = true
				if routers[key] {
					continue
				}
				verb, ok := leafVerbs[key]
				if !ok {
					t.Errorf("CLI leaf %s has no launch control verb; add it to the verb table and this map", key)
					continue
				}
				if _, ok := launchcontrol.Classify(verb); !ok {
					t.Errorf("CLI leaf %s maps to unclassified verb %s", key, verb)
				}
			}
			return true
		})
	}
	for key := range leafVerbs {
		if !seen[key] {
			t.Errorf("verb map names %s but flow.go has no such case", key)
		}
	}
	covered := map[launchcontrol.Verb]bool{}
	for _, verb := range leafVerbs {
		covered[verb] = true
	}
	for _, verb := range launchcontrol.AllVerbs() {
		if !covered[verb] {
			t.Errorf("verb %s has no CLI leaf", verb)
		}
	}
	if len(seen) == 0 || !strings.Contains(strings.Join(keys(seen), ","), "runFlowPhase/complete") {
		t.Fatalf("parsed leaves = %v", seen)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
