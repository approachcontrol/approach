package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/actions"
)

type flowLaunchFunctionContract struct {
	calls       map[string]bool
	identifiers map[string]bool
}

type flowLaunchFunctionKey struct {
	receiver string
	name     string
}

func modelFlowLaunchFunction(name string) flowLaunchFunctionKey {
	return flowLaunchFunctionKey{receiver: "Model", name: name}
}

func flowLaunchReceiverName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return flowLaunchReceiverName(expr.X)
	default:
		return ""
	}
}

func TestGenericAgentLaunchRequestRejectsEveryFlowContextMarker(t *testing.T) {
	tests := map[string]func(*actions.AgentLaunchContext){
		"FlowID":                 func(ctx *actions.AgentLaunchContext) { ctx.FlowID = "flow-1" },
		"FlowPhaseID":            func(ctx *actions.AgentLaunchContext) { ctx.FlowPhaseID = "plan" },
		"FlowPhaseKind":          func(ctx *actions.AgentLaunchContext) { ctx.FlowPhaseKind = "plan" },
		"FlowLaunchTracked":      func(ctx *actions.AgentLaunchContext) { ctx.FlowLaunchTracked = true },
		"FlowAutoLaunch":         func(ctx *actions.AgentLaunchContext) { ctx.FlowAutoLaunch = true },
		"FlowRepair":             func(ctx *actions.AgentLaunchContext) { ctx.FlowRepair = true },
		"FlowAgent":              func(ctx *actions.AgentLaunchContext) { ctx.FlowAgent = true },
		"FlowSavedSessionResume": func(ctx *actions.AgentLaunchContext) { ctx.FlowSavedSessionResume = true },
		"FlowAutofix":            func(ctx *actions.AgentLaunchContext) { ctx.FlowAutofix = true },
		"FlowPhaseTerminal":      func(ctx *actions.AgentLaunchContext) { ctx.FlowPhaseTerminal = true },
	}
	for name, mark := range tests {
		t.Run(name, func(t *testing.T) {
			m := NewWithOptions(nil, Options{})
			ctx := actions.AgentLaunchContext{LaunchID: "launch-1"}
			mark(&ctx)
			released := 0
			next, cmd := m.launchAgentForBackend(ctx, func() { released++ })
			if cmd != nil {
				t.Fatalf("generic Flow-bearing launch returned command %T", cmd)
			}
			if released != 1 {
				t.Fatalf("generic Flow-bearing launch released reservation %d times", released)
			}
			if !strings.Contains(next.status.Text, "Flow launch lifecycle") {
				t.Fatalf("generic Flow-bearing launch status = %q", next.status.Text)
			}
		})
	}
}

func flowLaunchFunctionContractForBody(body *ast.BlockStmt) flowLaunchFunctionContract {
	contract := flowLaunchFunctionContract{calls: make(map[string]bool), identifiers: make(map[string]bool)}
	aliases := make(map[string]map[string]bool)
	addAlias := func(name string, value ast.Expr) {
		if name == "" {
			return
		}
		targets := flowLaunchCallableNames(value, aliases)
		if len(targets) == 0 {
			return
		}
		if aliases[name] == nil {
			aliases[name] = make(map[string]bool)
		}
		for target := range targets {
			aliases[name][target] = true
		}
	}
	// Collect local function-value aliases first. Alias scopes are deliberately
	// conservative: retaining every possible target makes the boundary guard
	// fail closed across reassignments instead of letting a later call disappear.
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) == len(node.Rhs) {
				for i, left := range node.Lhs {
					if name, ok := left.(*ast.Ident); ok {
						addAlias(name.Name, node.Rhs[i])
					}
				}
			}
		case *ast.ValueSpec:
			if len(node.Names) == len(node.Values) {
				for i, name := range node.Names {
					addAlias(name.Name, node.Values[i])
				}
			}
		}
		return true
	})
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Ident:
			contract.identifiers[node.Name] = true
		case *ast.CallExpr:
			for called := range flowLaunchCallableNames(node.Fun, aliases) {
				contract.calls[called] = true
			}
		}
		return true
	})
	return contract
}

func flowLaunchCallableNames(expr ast.Expr, aliases map[string]map[string]bool) map[string]bool {
	names := make(map[string]bool)
	switch expr := expr.(type) {
	case *ast.Ident:
		if targets := aliases[expr.Name]; len(targets) != 0 {
			for target := range targets {
				names[target] = true
			}
		} else {
			names[expr.Name] = true
		}
	case *ast.SelectorExpr:
		names[expr.Sel.Name] = true
	case *ast.ParenExpr:
		return flowLaunchCallableNames(expr.X, aliases)
	}
	return names
}

func TestFlowLaunchFunctionContractTracksFunctionValueAliases(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "alias.go", `package model
func bypass(m Model, ctx launchContext) {
	open := m.openFlowEmbeddedTerminalReserved
	open(ctx)
}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	function := file.Decls[0].(*ast.FuncDecl)
	contract := flowLaunchFunctionContractForBody(function.Body)
	if !contract.calls["openFlowEmbeddedTerminalReserved"] {
		t.Fatal("function-value alias hid openFlowEmbeddedTerminalReserved from the launch boundary contract")
	}
}

func TestFlowLaunchFunctionContractTracksSinkPassedAsArgument(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "argument.go", `package model
func bypass(m Model, ctx launchContext) {
	invoke(m.openFlowEmbeddedTerminalReserved, ctx)
}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	function := file.Decls[0].(*ast.FuncDecl)
	key := flowLaunchFunctionKey{name: function.Name.Name}
	contracts := map[flowLaunchFunctionKey]flowLaunchFunctionContract{
		key: flowLaunchFunctionContractForBody(function.Body),
	}
	if !contractSinkReferences(contracts, "openFlowEmbeddedTerminalReserved")[key] {
		t.Fatal("function argument hid openFlowEmbeddedTerminalReserved from the launch boundary inventory")
	}
}

func parseFlowLaunchFunctionContracts(t *testing.T, includeTests bool) map[flowLaunchFunctionKey]flowLaunchFunctionContract {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	contracts := make(map[flowLaunchFunctionKey]flowLaunchFunctionContract)
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
			contract := flowLaunchFunctionContractForBody(function.Body)
			key := flowLaunchFunctionKey{name: function.Name.Name}
			if function.Recv != nil && len(function.Recv.List) == 1 {
				key.receiver = flowLaunchReceiverName(function.Recv.List[0].Type)
			}
			if _, exists := contracts[key]; exists {
				t.Fatalf("duplicate function contract for receiver=%q name=%q", key.receiver, key.name)
			}
			contracts[key] = contract
		}
	}
	return contracts
}

func flowLaunchContractTargets(contracts map[flowLaunchFunctionKey]flowLaunchFunctionContract, name string) []flowLaunchFunctionKey {
	targets := make([]flowLaunchFunctionKey, 0, 1)
	for key := range contracts {
		if key.name == name {
			targets = append(targets, key)
		}
	}
	return targets
}

func contractReaches(contracts map[flowLaunchFunctionKey]flowLaunchFunctionContract, source, target flowLaunchFunctionKey) bool {
	seen := make(map[flowLaunchFunctionKey]bool)
	var visit func(flowLaunchFunctionKey) bool
	visit = func(key flowLaunchFunctionKey) bool {
		if key == target {
			return true
		}
		if seen[key] {
			return false
		}
		seen[key] = true
		for called := range contracts[key].calls {
			// A bare AST selector does not carry type information. Conservatively
			// traverse every same-named package function or method instead of
			// choosing one and risking a false-negative boundary pass.
			for _, candidate := range flowLaunchContractTargets(contracts, called) {
				if visit(candidate) {
					return true
				}
			}
		}
		return false
	}
	return visit(source)
}

func contractReachableIdentifier(contracts map[flowLaunchFunctionKey]flowLaunchFunctionContract, source flowLaunchFunctionKey, identifier string) bool {
	seen := make(map[flowLaunchFunctionKey]bool)
	var visit func(flowLaunchFunctionKey) bool
	visit = func(key flowLaunchFunctionKey) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		contract := contracts[key]
		if contract.identifiers[identifier] {
			return true
		}
		for called := range contract.calls {
			for _, candidate := range flowLaunchContractTargets(contracts, called) {
				if visit(candidate) {
					return true
				}
			}
		}
		return false
	}
	return visit(source)
}

func contractSinkBeforeBoundary(contracts map[flowLaunchFunctionKey]flowLaunchFunctionContract, source, boundary flowLaunchFunctionKey, sinks map[string]bool) string {
	seen := make(map[flowLaunchFunctionKey]bool)
	exempt := map[string]bool{
		"routeNonFlowSavedSessionResume": true,
		"createReadyBeadFlowOnly":        true,
	}
	var visit func(flowLaunchFunctionKey) string
	visit = func(key flowLaunchFunctionKey) string {
		if key == boundary || seen[key] {
			return ""
		}
		seen[key] = true
		for called := range contracts[key].calls {
			if exempt[called] {
				continue
			}
			if sinks[called] {
				return called
			}
			for _, candidate := range flowLaunchContractTargets(contracts, called) {
				if found := visit(candidate); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return visit(source)
}

func declaredFlowLaunchKinds(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "flow_launch_intent.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]bool)
	for _, declaration := range file.Decls {
		generic, ok := declaration.(*ast.GenDecl)
		if !ok || generic.Tok != token.CONST {
			continue
		}
		for _, spec := range generic.Specs {
			values := spec.(*ast.ValueSpec)
			for _, name := range values.Names {
				if strings.HasPrefix(name.Name, "flowLaunchKind") {
					kinds[name.Name] = true
				}
			}
		}
	}
	return kinds
}

func contractSinkReferences(contracts map[flowLaunchFunctionKey]flowLaunchFunctionContract, sink string) map[flowLaunchFunctionKey]bool {
	references := make(map[flowLaunchFunctionKey]bool)
	for key, contract := range contracts {
		if contract.identifiers[sink] {
			references[key] = true
		}
	}
	return references
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
		"CreateWithOptions":                         true,
		"CreateFlow":                                true,
		"ReserveAgentLaunch":                        true,
		"ReserveLaunch":                             true,
		"AddPhaseLaunchID":                          true,
		"SetStartMetadata":                          true,
		"openFlowEmbeddedTerminal":                  true,
		"openFlowEmbeddedTerminalReserved":          true,
		"runAgentLaunchWithStatus":                  true,
		"launchAgentForBackend":                     true,
		"launchAgentWithContextReservation":         true,
		"launchAgentWithContextStatus":              true,
		"launchAgentInRepoTmuxSession":              true,
		"resumeSessionInEmbeddedTerminal":           true,
		"resumeSessionInEmbeddedTerminalWithStatus": true,
		"openEmbeddedTerminal":                      true,
		"launchAgentAtPath":                         true,
		"launchAgentAtPathWithBranch":               true,
	}
	// This inverted inventory is the fail-closed half of the contract: every
	// production caller of a persistence or runtime sink must remain one of the
	// reviewed lifecycle adapters or an explicitly non-Flow neighboring route.
	// Adding a new wrapper or source fails even when it declares no intent kind.
	wiring := flowLaunchFunctionKey{name: "NewWithOptions"}
	creatorWiring := flowLaunchFunctionKey{name: "newFlowCreator"}
	lifecycleWiring := flowLaunchFunctionKey{name: "newFlowLaunchSeams"}
	expectedSinkReferences := map[string]map[flowLaunchFunctionKey]bool{
		"CreateWithOptions":                         {wiring: true},
		"CreateFlow":                                {wiring: true, creatorWiring: true, {name: "createFlowLaunchWriteCmd"}: true},
		"ReserveAgentLaunch":                        {wiring: true},
		"ReserveLaunch":                             {wiring: true, creatorWiring: true, {name: "createFlowLaunchReserveCmd"}: true},
		"AddPhaseLaunchID":                          {wiring: true, lifecycleWiring: true, {name: "createFlowLaunchIDCmd"}: true, modelFlowLaunchFunction("flowLaunchLauncher"): true, modelFlowLaunchFunction("phaseResumeFlowLaunchPrepareCmd"): true},
		"SetStartMetadata":                          {wiring: true, creatorWiring: true, {name: "createFlowLaunchMetadataCmd"}: true, {name: "createFlowLaunchRecoveryCmd"}: true},
		"openFlowEmbeddedTerminal":                  {},
		"openFlowEmbeddedTerminalReserved":          {modelFlowLaunchFunction("openFlowEmbeddedTerminal"): true, modelFlowLaunchFunction("installFlowLaunchEmbedded"): true},
		"runAgentLaunchWithStatus":                  {modelFlowLaunchFunction("launchAgentInRepoTmuxSession"): true, modelFlowLaunchFunction("handoffFlowLaunchTmux"): true, modelFlowLaunchFunction("launchAgentWithContextStatus"): true, modelFlowLaunchFunction("runAgentLaunchWithReservation"): true},
		"launchAgentForBackend":                     {modelFlowLaunchFunction("Update"): true, modelFlowLaunchFunction("routeNonFlowSavedSessionResume"): true, modelFlowLaunchFunction("launchAgentAtPath"): true, modelFlowLaunchFunction("launchAgentAtPathWithBranch"): true},
		"launchAgentWithContextReservation":         {},
		"launchAgentWithContextStatus":              {modelFlowLaunchFunction("launchAgentForBackend"): true, modelFlowLaunchFunction("launchAgentWithContextReservation"): true, modelFlowLaunchFunction("resumeSessionInEmbeddedTerminalWithStatus"): true},
		"launchAgentInRepoTmuxSession":              {modelFlowLaunchFunction("launchAgentForBackend"): true, modelFlowLaunchFunction("resumeSessionForBackend"): true},
		"resumeSessionInEmbeddedTerminal":           {modelFlowLaunchFunction("resumeSessionForBackend"): true},
		"resumeSessionInEmbeddedTerminalWithStatus": {modelFlowLaunchFunction("resumeSessionInEmbeddedTerminal"): true, modelFlowLaunchFunction("resumeSessionForBackend"): true},
		"openEmbeddedTerminal":                      {modelFlowLaunchFunction("resumeSessionInEmbeddedTerminalWithStatus"): true},
		"launchAgentAtPath":                         {modelFlowLaunchFunction("handleOpenAgent"): true},
		"launchAgentAtPathWithBranch":               {modelFlowLaunchFunction("handleWorktreeCreated"): true},
	}
	for sink := range sinks {
		if got, want := contractSinkReferences(contracts, sink), expectedSinkReferences[sink]; !reflect.DeepEqual(got, want) {
			t.Errorf("production references to launch sink %s changed: got=%v want=%v", sink, got, want)
		}
	}
	for _, source := range sources {
		t.Run(source.name, func(t *testing.T) {
			sourceKey := modelFlowLaunchFunction(source.name)
			if _, ok := contracts[sourceKey]; !ok {
				t.Fatalf("launch source %s is missing", source.name)
			}
			if !contractReachableIdentifier(contracts, sourceKey, source.kind) {
				t.Fatalf("%s does not identify %s", source.name, source.kind)
			}
			if !contractReaches(contracts, sourceKey, modelFlowLaunchFunction("requestFlowLaunch")) {
				t.Fatalf("%s does not reach requestFlowLaunch", source.name)
			}
			if sink := contractSinkBeforeBoundary(contracts, sourceKey, modelFlowLaunchFunction("requestFlowLaunch"), sinks); sink != "" {
				t.Fatalf("%s reaches launch sink %s before requestFlowLaunch", source.name, sink)
			}
		})
	}

	for _, source := range []string{"handleResumeSession", "handleEmbeddedSessionPickerSelected", "handleEnter"} {
		if !contractReaches(contracts, modelFlowLaunchFunction(source), modelFlowLaunchFunction("routeSavedSessionResume")) {
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
		sourceKey := modelFlowLaunchFunction(source.name)
		contract := contracts[sourceKey]
		if !contract.identifiers["flowLaunchCreateRequestedMsg"] || !contract.identifiers[source.origin] {
			t.Errorf("%s does not emit a %s create request", source.name, source.origin)
		}
		if sink := contractSinkBeforeBoundary(contracts, sourceKey, modelFlowLaunchFunction("requestFlowLaunch"), sinks); sink != "" {
			t.Errorf("%s reaches launch sink %s before lifecycle admission", source.name, sink)
		}
	}
	update := modelFlowLaunchFunction("Update")
	if !contractReachableIdentifier(contracts, update, "flowLaunchKindCreatePhase") ||
		!contractReaches(contracts, update, modelFlowLaunchFunction("requestFlowLaunch")) {
		t.Fatal("flowLaunchCreateRequestedMsg is not funneled through createPhase admission")
	}
	if !contracts[update].identifiers["flowLaunchEventMsg"] ||
		!contractReaches(contracts, update, modelFlowLaunchFunction("handleFlowLaunchEvent")) {
		t.Fatal("flowLaunchEventMsg is not funneled through handleFlowLaunchEvent")
	}

	coveredKinds := map[string]bool{"flowLaunchKindCreatePhase": true}
	for _, source := range sources {
		coveredKinds[source.kind] = true
	}
	if declared := declaredFlowLaunchKinds(t); !reflect.DeepEqual(declared, coveredKinds) {
		t.Fatalf("Flow launch kind inventory changed: declared=%v covered=%v", declared, coveredKinds)
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
		"AgentLaunch" + "RequestedMsg":           true,
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
