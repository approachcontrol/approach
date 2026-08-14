package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type flowLaunchFunctionContract struct {
	calls       map[string]bool
	identifiers map[string]bool
}

func parseFlowLaunchFunctionContracts(t *testing.T, includeTests bool) map[string]flowLaunchFunctionContract {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	contracts := make(map[string]flowLaunchFunctionContract)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || (!includeTests && strings.HasSuffix(name, "_test.go")) {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			contract := flowLaunchFunctionContract{calls: make(map[string]bool), identifiers: make(map[string]bool)}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.Ident:
					contract.identifiers[node.Name] = true
				case *ast.CallExpr:
					switch called := node.Fun.(type) {
					case *ast.Ident:
						contract.calls[called.Name] = true
					case *ast.SelectorExpr:
						contract.calls[called.Sel.Name] = true
					}
				}
				return true
			})
			contracts[function.Name.Name] = contract
		}
	}
	return contracts
}

func contractReaches(contracts map[string]flowLaunchFunctionContract, source, target string) bool {
	seen := make(map[string]bool)
	var visit func(string) bool
	visit = func(name string) bool {
		if name == target {
			return true
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		for called := range contracts[name].calls {
			if visit(called) {
				return true
			}
		}
		return false
	}
	return visit(source)
}

func contractReachableIdentifier(contracts map[string]flowLaunchFunctionContract, source, identifier string) bool {
	seen := make(map[string]bool)
	var visit func(string) bool
	visit = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		contract := contracts[name]
		if contract.identifiers[identifier] {
			return true
		}
		for called := range contract.calls {
			if visit(called) {
				return true
			}
		}
		return false
	}
	return visit(source)
}

func contractSinkBeforeBoundary(contracts map[string]flowLaunchFunctionContract, source, boundary string, sinks map[string]bool) string {
	seen := make(map[string]bool)
	exempt := map[string]bool{
		"routeNonFlowSavedSessionResume": true,
		"createReadyBeadFlowOnly":        true,
	}
	var visit func(string) string
	visit = func(name string) string {
		if name == boundary || seen[name] {
			return ""
		}
		seen[name] = true
		for called := range contracts[name].calls {
			if exempt[called] {
				continue
			}
			if sinks[called] {
				return called
			}
			if found := visit(called); found != "" {
				return found
			}
		}
		return ""
	}
	return visit(source)
}

func TestFlowLaunchLifecycleBoundary(t *testing.T) {
	contracts := parseFlowLaunchFunctionContracts(t, false)
	sources := []struct {
		name string
		kind string
	}{
		{name: "handleLaunchNextFlowPhase", kind: "flowLaunchKindManualPhase"},
		{name: "prepareAutoAdvanceDrainLaunches", kind: "flowLaunchKindAutoPhase"},
		{name: "handleResumeFlowPhaseSession", kind: "flowLaunchKindPhaseResume"},
		{name: "handleRepairSelectedFlow", kind: "flowLaunchKindRepair"},
		{name: "handleStartSelectedFlowWorktreeAgent", kind: "flowLaunchKindWorktreeAgent"},
		{name: "routeSavedSessionResume", kind: "flowLaunchKindSavedSessionResume"},
		{name: "handleAutofixSelectedFlowPR", kind: "flowLaunchKindAutofix"},
	}
	sinks := map[string]bool{
		"CreateWithOptions":                 true,
		"CreateFlow":                        true,
		"ReserveAgentLaunch":                true,
		"ReserveLaunch":                     true,
		"AddPhaseLaunchID":                  true,
		"SetStartMetadata":                  true,
		"openFlowEmbeddedTerminal":          true,
		"openFlowEmbeddedTerminalReserved":  true,
		"runAgentLaunchWithStatus":          true,
		"launchAgentForBackend":             true,
		"launchAgentWithContextReservation": true,
		"launchAgentWithContextStatus":      true,
		"launchAgentInRepoTmuxSession":      true,
	}
	for _, source := range sources {
		t.Run(source.name, func(t *testing.T) {
			if _, ok := contracts[source.name]; !ok {
				t.Fatalf("launch source %s is missing", source.name)
			}
			if !contractReachableIdentifier(contracts, source.name, source.kind) {
				t.Fatalf("%s does not identify %s", source.name, source.kind)
			}
			if !contractReaches(contracts, source.name, "requestFlowLaunch") {
				t.Fatalf("%s does not reach requestFlowLaunch", source.name)
			}
			if sink := contractSinkBeforeBoundary(contracts, source.name, "requestFlowLaunch", sinks); sink != "" {
				t.Fatalf("%s reaches launch sink %s before requestFlowLaunch", source.name, sink)
			}
		})
	}

	for _, source := range []string{"handleResumeSession", "handleEmbeddedSessionPickerSelected", "handleEnter"} {
		if !contractReaches(contracts, source, "routeSavedSessionResume") {
			t.Errorf("saved-session surface %s does not reach routeSavedSessionResume", source)
		}
	}

	createSources := []struct {
		name   string
		origin string
	}{
		{name: "createFlowAndLaunchPlanForRepo", origin: "flowLaunchOriginNewFlow"},
		{name: "requestReadyBeadFlowLaunch", origin: "flowLaunchOriginReadyBead"},
	}
	for _, source := range createSources {
		contract := contracts[source.name]
		if !contract.identifiers["flowLaunchCreateRequestedMsg"] || !contract.identifiers[source.origin] {
			t.Errorf("%s does not emit a %s create request", source.name, source.origin)
		}
		if sink := contractSinkBeforeBoundary(contracts, source.name, "requestFlowLaunch", sinks); sink != "" {
			t.Errorf("%s reaches launch sink %s before lifecycle admission", source.name, sink)
		}
	}
	if !contractReachableIdentifier(contracts, "Update", "flowLaunchKindCreatePhase") ||
		!contractReaches(contracts, "Update", "requestFlowLaunch") {
		t.Fatal("flowLaunchCreateRequestedMsg is not funneled through createPhase admission")
	}
}

func TestFlowLaunchLegacySymbolsAbsent(t *testing.T) {
	forbidden := map[string]bool{
		"Flow" + "Starter":                       true,
		"FlowPhase" + "Launcher":                 true,
		"StartFlow" + "Plan":                     true,
		"FlowEmbedded" + "LaunchRequestedMsg":    true,
		"PlanLaunch" + "RequestedMsg":            true,
		"flowPlan" + "LaunchMessage":             true,
		"readyBeadFlowPlan" + "LaunchMessage":    true,
		"launchFlow" + "EmbeddedRequest":         true,
		"launchTrackedFlow" + "Embedded":         true,
		"acceptCreationTime" + "FlowLaunch":      true,
		"rejectStaleCreationTime" + "FlowLaunch": true,
		"flowLaunchLeases":                       true,
		"pendingFlowLaunches":                    true,
		"pendingFlowRepairLaunches":              true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && forbidden[identifier.Name] {
				t.Errorf("legacy launch symbol %s remains in %s", identifier.Name, entry.Name())
			}
			return true
		})
	}

	file, err := parser.ParseFile(fset, "flow_start.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "FlowStartResult" {
			return true
		}
		structure, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			t.Fatal("FlowStartResult is not a struct")
		}
		for _, field := range structure.Fields.List {
			for _, name := range field.Names {
				switch name.Name {
				case "Flow", "Worktree", "Commit":
				default:
					t.Errorf("launch-only FlowStartResult field remains: %s", name.Name)
				}
			}
		}
		return false
	})
}
