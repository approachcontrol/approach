package agentskills

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestWtuiFlowSkillDocumentsAgentContract(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow", "SKILL.md"))

	requireContainsAll(t, "skill metadata", skill, []string{
		"name: wtui-flow",
		"WTUI_FLOW_ID",
		"WTUI_FLOW_PHASE_ID",
	})
	requireContainsAll(t, "flow commands", skill, []string{
		"wtui flow read --flow-id",
		"wtui flow phase complete",
		"wtui flow phase block",
		"wtui flow phase needs-attention",
		"wtui flow phase restart",
		"wtui flow phase set",
		"wtui flow plan set",
		"wtui flow pr set",
		"wtui plan save",
		"wtui plan phase set",
		"wtui plan read",
	})
	requireContainsAll(t, "default phase playbooks", skill, []string{
		"plan",
		"plan-review",
		"implementation",
		"review-loop-1",
		"review-loop-2",
		"pr-creation",
		"merge",
	})
	requireContainsAll(t, "agent-facing phase statuses", skill, []string{
		"running",
		"needs_attention",
		"completed",
		"blocked",
		"skipped",
		"ready",
		"cannot set",
	})
	requireContainsAll(t, "flow outcomes and failure handling", skill, []string{
		"--outcome",
		"--summary",
		"--notes",
		"persistence failures",
		"must not be treated as successful phase progression",
	})
}

func TestWtuiFlowSkillMatchesImplementedFlowCLIContract(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow", "SKILL.md"))
	flowCLI := readFile(t, filepath.Join(root, "cmd", "wtui", "flow.go"))
	planCLI := readFile(t, filepath.Join(root, "cmd", "wtui", "plan.go"))
	flowStore := readFile(t, filepath.Join(root, "flowstore", "store.go"))

	if !strings.Contains(skill, "wtui flow phase set") || !strings.Contains(flowCLI, "runFlowPhaseSet") {
		t.Fatal("skill and CLI should both expose flow phase set")
	}
	if !strings.Contains(skill, "wtui flow phase complete") || !strings.Contains(flowCLI, `command:        "complete"`) {
		t.Fatal("skill and CLI should both expose flow phase complete")
	}
	if !strings.Contains(skill, "wtui flow phase block") || !strings.Contains(flowCLI, `command:        "block"`) {
		t.Fatal("skill and CLI should both expose flow phase block")
	}
	if !strings.Contains(skill, "wtui flow phase needs-attention") || !strings.Contains(flowCLI, `command:        "needs-attention"`) {
		t.Fatal("skill and CLI should both expose flow phase needs-attention")
	}
	if !strings.Contains(skill, "wtui flow phase restart") || !strings.Contains(flowCLI, "runFlowPhaseRestart") {
		t.Fatal("skill and CLI should both expose flow phase restart")
	}
	if !strings.Contains(skill, "wtui flow plan set") || !strings.Contains(flowCLI, "runFlowPlanSet") {
		t.Fatal("skill and CLI should both expose flow plan set")
	}
	if !strings.Contains(skill, "wtui flow pr set") || !strings.Contains(flowCLI, "runFlowPRSet") {
		t.Fatal("skill and CLI should both expose flow pr set")
	}
	if !strings.Contains(skill, "wtui flow merge set") || !strings.Contains(flowCLI, "runFlowMergeSet") {
		t.Fatal("skill and CLI should both expose flow merge set")
	}
	for _, flagName := range []string{
		"flow-id",
		"phase-id",
		"plan-id",
		"provider",
		"number",
		"url",
		"head",
		"base",
		"status",
		"commit",
		"merged-at",
		"outcome",
		"summary",
		"notes",
		"state-root",
	} {
		hasStringFlag := strings.Contains(flowCLI, `flags.String("`+flagName+`"`)
		hasIntFlag := strings.Contains(flowCLI, `flags.Int("`+flagName+`"`)
		if strings.Contains(skill, "--"+flagName) && !hasStringFlag && !hasIntFlag {
			t.Fatalf("skill documents --%s but flow CLI does not expose it", flagName)
		}
	}
	assertRunnableExampleFlagsExist(t, skill, flowCLI, "flow")
	assertRunnableExampleFlagsExist(t, skill, planCLI, "plan")

	for _, constant := range []string{
		"PhaseRunning",
		"PhaseNeedsAttention",
		"PhaseCompleted",
		"PhaseBlocked",
		"PhaseSkipped",
		"PhaseReady",
		"StatusPending",
		"StatusInProgress",
		"StatusNeedsAttention",
		"StatusBlocked",
		"StatusCompleted",
		"StatusMerged",
		"StatusAbandoned",
		"MergePending",
		"MergeMerged",
		"MergeBlocked",
	} {
		if !strings.Contains(flowStore, constant) {
			t.Fatalf("flowstore contract missing %s", constant)
		}
	}

	for _, unimplementedCommand := range []string{
		"wtui flow session attach",
		"wtui flow abandon",
	} {
		if hasRunnableCommandExample(skill, unimplementedCommand) {
			t.Fatalf("skill includes a runnable example for unimplemented command %q", unimplementedCommand)
		}
	}
}

func TestWtuiFlowCreateSkillDocumentsAgentContract(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow-create", "SKILL.md"))

	requireContainsAll(t, "skill metadata", skill, []string{
		"name: wtui-flow-create",
		"make a flow from this",
		"add this as a wtui flow",
		"create a wtui flow for this plan",
	})
	requireContainsAll(t, "independent flow creation", skill, []string{
		"does not require `WTUI_FLOW_ID` or `WTUI_FLOW_PHASE_ID`",
		"Codex App prompt-only metadata",
		"ask the user",
		"absolute",
	})
	requireContainsAll(t, "shared artifact root setup", skill, []string{
		"WTUI_ARTIFACT_ROOT",
		"WTUI_FLOW_STATE_ROOT",
		"WTUI_PLAN_STATE_ROOT",
		"WTUI_SESSION_STATE_ROOT",
		"FLOW_STATE_ARGS",
		"PLAN_STATE_ARGS",
	})
	requireContainsAll(t, "flow creation commands", skill, []string{
		"wtui flow create",
		"--json",
		"--instructions-file",
		"--instructions",
		"wtui flow read --flow-id",
	})
	requireContainsAll(t, "plan import commands", skill, []string{
		"wtui plan save",
		"wtui flow plan set",
		"wtui plan read",
		"wtui flow phase complete",
		"Imported plan",
	})
	requireContainsAll(t, "failure handling", skill, []string{
		"persistence failures",
		"must not be treated as success",
		"report the command error",
		"wtui flow phase block",
		"wtui flow phase needs-attention",
	})

	if hasRunnableCommandExample(skill, "wtui flow session attach") {
		t.Fatal("skill includes a runnable example for unimplemented command \"wtui flow session attach\"")
	}
}

func TestWtuiFlowCreateSkillGuardsPersistenceFailures(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow-create", "SKILL.md"))
	createBlock := fencedBashBlockContaining(t, skill, "FLOW_JSON=$(wtui flow create")
	importBlock := fencedBashBlockContaining(t, skill, "PLAN_ID=$(printf")

	requireContainsAll(t, "flow creation persistence guards", skill, []string{
		"if ! FLOW_JSON=$(wtui flow create",
		"if ! FLOW_ID=$(printf '%s' \"$FLOW_JSON\" | python3",
		"if ! wtui flow read --flow-id \"$FLOW_ID\"",
		"case \"${WTUI_REPO_PATH:-}\" in",
		"absolute WTUI_REPO_PATH",
		"exit 1",
	})
	assertGuardedFailureBodies(t, createBlock, "if ! FLOW_JSON=$(wtui flow create", 2, []string{
		"wtui flow create failed",
		"exit 1",
	})
	assertGuardedFailureBodies(t, createBlock, "if ! FLOW_ID=$(printf", 1, []string{
		"flow_id",
		"exit 1",
	})
	assertGuardedFailureBodies(t, createBlock, `if ! wtui flow read --flow-id "$FLOW_ID"`, 1, []string{
		"wtui flow read failed",
		"exit 1",
	})

	requireContainsAll(t, "plan import persistence guards", skill, []string{
		"record_plan_import_failure",
		"if ! PLAN_ID=$(printf '%s' \"${PLAN_MARKDOWN:-}\" | wtui plan save",
		"if ! wtui flow plan set",
		"if ! wtui plan read --plan-id \"$PLAN_ID\"",
		"if ! wtui flow phase complete",
		"wtui flow phase block failed after plan import failure",
		"exit 1",
	})
	for _, guard := range []string{
		"if ! PLAN_ID=$(printf",
		"if ! wtui flow plan set",
		`if ! wtui plan read --plan-id "$PLAN_ID"`,
		"if ! wtui flow phase complete",
	} {
		assertGuardedFailureBodies(t, importBlock, guard, 1, []string{
			"record_plan_import_failure",
			"exit 1",
		})
	}

	readbackIndex := strings.Index(skill, `if ! wtui plan read --plan-id "$PLAN_ID"`)
	completeIndex := strings.Index(skill, "if ! wtui flow phase complete")
	if readbackIndex < 0 || completeIndex < 0 || completeIndex < readbackIndex {
		t.Fatal("skill should guard plan readback before attempting to complete the Flow plan phase")
	}
}

func TestWtuiFlowCreateSkillExamplesAreUnsetSafe(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow-create", "SKILL.md"))

	requireContainsAll(t, "unset-safe shell variables", skill, []string{
		"${FLOW_TITLE:-}",
		"${FLOW_INSTRUCTIONS_FILE:-}",
		"${FLOW_INSTRUCTIONS:-}",
		"${WTUI_REPO_PATH:-}",
		"${WTUI_WORKTREE_PATH:-}",
		"${WTUI_BRANCH:-}",
		"${WTUI_COMMIT:-}",
		"${FLOW_ID:-}",
		"${PLAN_MARKDOWN:-}",
	})
}

func TestWtuiFlowCreateSkillMatchesImplementedCLIContract(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow-create", "SKILL.md"))
	flowCLI := readFile(t, filepath.Join(root, "cmd", "wtui", "flow.go"))
	planCLI := readFile(t, filepath.Join(root, "cmd", "wtui", "plan.go"))

	requireContainsAll(t, "flow CLI contract", flowCLI, []string{
		"runFlowCreate",
		"runFlowRead",
		"runFlowPlanSet",
		`command:        "complete"`,
		`command:        "block"`,
		`command:        "needs-attention"`,
	})
	requireContainsAll(t, "plan CLI contract", planCLI, []string{
		"runPlanSave",
		"runPlanRead",
	})

	assertRunnableExampleFlagsExist(t, skill, flowCLI, "flow")
	assertRunnableExampleFlagsExist(t, skill, planCLI, "plan")
}

func TestWtuiFlowSkillKeepsPlanAndFlowStateRootsTogether(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow", "SKILL.md"))

	assertSkillKeepsPlanAndFlowStateRootsTogether(t, skill)
}

func TestWtuiFlowCreateSkillKeepsPlanAndFlowStateRootsTogether(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow-create", "SKILL.md"))

	assertSkillKeepsPlanAndFlowStateRootsTogether(t, skill)
}

func assertSkillKeepsPlanAndFlowStateRootsTogether(t *testing.T, skill string) {
	t.Helper()
	requireContainsAll(t, "shared artifact root setup", skill, []string{
		"WTUI_ARTIFACT_ROOT",
		"WTUI_FLOW_STATE_ROOT",
		"WTUI_PLAN_STATE_ROOT",
		"WTUI_SESSION_STATE_ROOT",
		"FLOW_STATE_ARGS",
		"PLAN_STATE_ARGS",
	})

	for _, block := range fencedBashBlocks(skill) {
		if strings.Contains(block, "wtui flow ") && !strings.Contains(block, `"${FLOW_STATE_ARGS[@]}"`) {
			t.Fatalf("flow example missing FLOW_STATE_ARGS:\n%s", block)
		}
		if strings.Contains(block, "wtui plan ") && !strings.Contains(block, `"${PLAN_STATE_ARGS[@]}"`) {
			t.Fatalf("plan example missing PLAN_STATE_ARGS:\n%s", block)
		}
	}
}

func TestWtuiFlowSkillPlanPhaseGuardsPersistenceFailures(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow", "SKILL.md"))

	requireContainsAll(t, "plan persistence guards", skill, []string{
		"if ! PLAN_ID=$(",
		"wtui flow plan set",
		`--plan-id "$PLAN_ID"`,
		`--outcome "plan_link_failed"`,
		`--outcome "plan_save_failed"`,
		`--outcome "plan_phase_save_failed"`,
		`--outcome "plan_read_failed"`,
		"exit 1",
	})
}

func TestWtuiFlowSkillHandlesMissingPlanID(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow", "SKILL.md"))

	requireContainsAll(t, "missing plan id guidance", skill, []string{
		`if [ -z "$WTUI_PLAN_ID" ]`,
		`if ! wtui plan read --plan-id "$WTUI_PLAN_ID" "${PLAN_STATE_ARGS[@]}"`,
		`--status blocked`,
		`--outcome "blocked"`,
		`wtui plan read --plan-id "$WTUI_PLAN_ID" "${PLAN_STATE_ARGS[@]}"`,
	})
}

func TestWtuiFlowSkillDocumentsPlanReviewGateOutcomes(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "wtui-flow", "SKILL.md"))

	requireContainsAll(t, "plan review outcome contract", skill, []string{
		"approved",
		"approved_with_concerns",
		"changes_requested",
		"blocked",
		"wtui derives all phase readiness",
		`wtui flow phase needs-attention --notes "..."`,
		`wtui flow phase complete --outcome "approved_with_concerns" --notes "..."`,
		`wtui flow phase block --notes "..."`,
	})
}

func TestWtuiFlowInstallationDocs(t *testing.T) {
	root := repoRoot(t)
	readme := readFile(t, filepath.Join(root, "README.md"))
	configDocs := readFile(t, filepath.Join(root, "docs", "config.md"))

	requireContainsAll(t, "README installation docs", readme, []string{
		"agent-skills/wtui-flow/",
		"agent-skills/wtui-flow-create/",
		"wtui-flow",
		"wtui-flow-create",
		"wtui-plan-persist",
		"symlink",
	})
	requireContainsAll(t, "config installation docs", configDocs, []string{
		"agent-skills/wtui-flow/",
		"agent-skills/wtui-flow-create/",
		"wtui-flow",
		"wtui-flow-create",
		"wtui-plan-persist",
		"symlink",
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireContainsAll(t *testing.T, label, haystack string, needles []string) {
	t.Helper()
	normalized := strings.Join(strings.Fields(haystack), " ")
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) && !strings.Contains(normalized, needle) {
			t.Fatalf("%s missing %q", label, needle)
		}
	}
}

func hasRunnableCommandExample(markdown, command string) bool {
	for _, block := range fencedBashBlocks(markdown) {
		for _, line := range strings.Split(block, "\n") {
			if strings.Contains(line, command) {
				return true
			}
		}
	}
	return false
}

func fencedBashBlockContaining(t *testing.T, markdown, text string) string {
	t.Helper()
	for _, block := range fencedBashBlocks(markdown) {
		if strings.Contains(block, text) {
			return block
		}
	}
	t.Fatalf("no fenced bash block contains %q", text)
	return ""
}

func assertGuardedFailureBodies(t *testing.T, block, guard string, wantCount int, wants []string) {
	t.Helper()
	searchFrom := 0
	for count := 0; count < wantCount; count++ {
		start := strings.Index(block[searchFrom:], guard)
		if start < 0 {
			t.Fatalf("guard %q occurrence %d missing in block:\n%s", guard, count+1, block)
		}
		start += searchFrom
		thenIndex := strings.Index(block[start:], "then")
		if thenIndex < 0 {
			t.Fatalf("guard %q occurrence %d missing then body", guard, count+1)
		}
		bodyStart := start + thenIndex + len("then")
		bodyEnd := guardedBodyEnd(block[bodyStart:])
		if bodyEnd < 0 {
			t.Fatalf("guard %q occurrence %d missing closing fi", guard, count+1)
		}
		body := block[bodyStart : bodyStart+bodyEnd]
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Fatalf("guard %q occurrence %d body missing %q:\n%s", guard, count+1, want, body)
			}
		}
		searchFrom = bodyStart + bodyEnd
	}
	if extra := strings.Index(block[searchFrom:], guard); extra >= 0 {
		t.Fatalf("guard %q has more than %d occurrences", guard, wantCount)
	}
}

func guardedBodyEnd(text string) int {
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.TrimSpace(line) == "fi" {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func assertRunnableExampleFlagsExist(t *testing.T, markdown, cliSource, command string) {
	t.Helper()
	for _, use := range runnableCommandFlagUses(markdown, command) {
		source, ok := commandFlagSource(cliSource, append([]string{command}, use.Subcommands...))
		if !ok {
			t.Fatalf("runnable %s example has no CLI contract mapping", use.Command())
		}
		if !cliHasFlag(source, use.FlagName) {
			t.Fatalf("runnable %s example documents --%s but that CLI command does not expose it", use.Command(), use.FlagName)
		}
	}
}

type runnableCommandFlagUse struct {
	TopCommand  string
	Subcommands []string
	FlagName    string
}

func (u runnableCommandFlagUse) Command() string {
	return "wtui " + strings.Join(append([]string{u.TopCommand}, u.Subcommands...), " ")
}

func runnableCommandFlagUses(markdown, command string) []runnableCommandFlagUse {
	var uses []runnableCommandFlagUse
	seen := map[string]bool{}
	flagPattern := regexp.MustCompile(`--([A-Za-z0-9][A-Za-z0-9-]*)`)

	for _, block := range fencedBashBlocks(markdown) {
		var activeSubcommands []string
		continues := false
		for _, line := range strings.Split(block, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				if !continues {
					activeSubcommands = nil
				}
				continue
			}

			if subcommands, ok := runnableWtuiSubcommands(trimmed, command); ok {
				activeSubcommands = subcommands
			} else if !continues {
				activeSubcommands = nil
			}

			if len(activeSubcommands) > 0 {
				unquoted := stripShellQuotedSpans(trimmed)
				for _, match := range flagPattern.FindAllStringSubmatch(unquoted, -1) {
					flagName := match[1]
					key := strings.Join(activeSubcommands, " ") + "\x00" + flagName
					if !seen[key] {
						uses = append(uses, runnableCommandFlagUse{
							TopCommand:  command,
							Subcommands: append([]string(nil), activeSubcommands...),
							FlagName:    flagName,
						})
						seen[key] = true
					}
				}
			}

			continues = strings.HasSuffix(trimmed, `\`)
		}
	}

	return uses
}

func stripShellQuotedSpans(line string) string {
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			b.WriteRune(' ')
			escaped = false
		case quote != '\'' && r == '\\':
			b.WriteRune(' ')
			escaped = true
		case quote != 0:
			b.WriteRune(' ')
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			b.WriteRune(' ')
			quote = r
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasRunnableWtuiCommand(line, command string) bool {
	_, ok := runnableWtuiSubcommands(line, command)
	return ok
}

func runnableWtuiSubcommands(line, command string) ([]string, bool) {
	pattern := "wtui " + command + " "
	index := strings.Index(line, pattern)
	if index < 0 {
		return nil, false
	}

	if index > 0 {
		prefix := line[:index]
		startsRunnableCommand := false
		for _, marker := range []string{"| ", "$(", "! "} {
			if strings.HasSuffix(prefix, marker) {
				startsRunnableCommand = true
				break
			}
		}
		if !startsRunnableCommand {
			return nil, false
		}
	}

	fields := strings.Fields(line[index:])
	if len(fields) < 3 || fields[0] != "wtui" || fields[1] != command {
		return nil, false
	}
	var subcommands []string
	for _, field := range fields[2:] {
		field = strings.Trim(field, `"'();`)
		if field == "" || field == `\` || strings.HasPrefix(field, "-") || strings.HasPrefix(field, "$") || strings.ContainsAny(field, "[]{}") {
			break
		}
		subcommands = append(subcommands, field)
	}
	if len(subcommands) == 0 {
		return nil, false
	}
	return subcommands, true
}

func commandFlagSource(cliSource string, commandParts []string) (string, bool) {
	functionName, ok := commandFunctionName(commandParts)
	if !ok {
		return "", false
	}
	return functionSource(cliSource, functionName)
}

func commandFunctionName(commandParts []string) (string, bool) {
	key := strings.Join(commandParts, " ")
	functions := map[string]string{
		"flow create":                "runFlowCreate",
		"flow read":                  "runFlowRead",
		"flow phase set":             "runFlowPhaseSet",
		"flow phase complete":        "runFlowPhaseAction",
		"flow phase block":           "runFlowPhaseAction",
		"flow phase needs-attention": "runFlowPhaseAction",
		"flow phase restart":         "runFlowPhaseRestart",
		"flow phase add-child":       "runFlowPhaseAddChild",
		"flow plan set":              "runFlowPlanSet",
		"flow pr set":                "runFlowPRSet",
		"flow merge set":             "runFlowMergeSet",
		"plan save":                  "runPlanSave",
		"plan read":                  "runPlanRead",
		"plan phase set":             "runPlanPhase",
	}
	functionName, ok := functions[key]
	return functionName, ok
}

func functionSource(source, functionName string) (string, bool) {
	start := strings.Index(source, "func "+functionName+"(")
	if start < 0 {
		return "", false
	}
	bodyStart := strings.Index(source[start:], "{")
	if bodyStart < 0 {
		return "", false
	}
	bodyStart += start
	depth := 0
	for i := bodyStart; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[start : i+1], true
			}
		}
	}
	return "", false
}

func cliHasFlag(cliSource, flagName string) bool {
	for _, flagType := range []string{"String", "Int", "Bool"} {
		if strings.Contains(cliSource, `flags.`+flagType+`("`+flagName+`"`) {
			return true
		}
	}
	return false
}

func fencedBashBlocks(markdown string) []string {
	var blocks []string
	var current []string
	inBash := false
	for _, line := range strings.Split(markdown, "\n") {
		switch {
		case strings.TrimSpace(line) == "```bash":
			inBash = true
			current = nil
		case inBash && strings.TrimSpace(line) == "```":
			blocks = append(blocks, strings.Join(current, "\n"))
			inBash = false
		case inBash:
			current = append(current, line)
		}
	}
	return blocks
}
