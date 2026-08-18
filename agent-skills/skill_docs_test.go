package agentskills

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestApproachFlowSkillDocumentsAgentContract(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow", "SKILL.md"))

	requireContainsAll(t, "skill metadata", skill, []string{
		"name: approach-flow",
		"APPROACH_FLOW_ID",
		"APPROACH_FLOW_PHASE_ID",
		"APPROACH_CURRENT_PHASE_ID",
	})
	requireContainsAll(t, "flow commands", skill, []string{
		"approach flow read --flow-id",
		"approach flow phase complete",
		"approach flow phase block",
		"approach flow phase needs-attention",
		"approach flow phase restart",
		"approach flow phase reset",
		"approach flow phase set",
		"approach flow plan set",
		"approach flow issue set",
		"approach flow pr set",
		"approach plan save",
		"approach plan phase set",
		"approach plan read",
	})
	requireContainsAll(t, "default phase playbooks", skill, []string{
		"plan",
		"plan-review",
		"implementation",
		"review-loop",
		"pr-creation",
		"autoreview",
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
	// The spooled contract: a deferred write is exit 0 with a fixed message,
	// reported as deferred and never retried.
	requireContainsAll(t, "spooled writes", skill, []string{
		"spooled: control endpoint unreachable and this build cannot open the flow database; the request will be applied on the next approach start",
		"do not retry",
		"APPROACH_CONTROL_ENDPOINT",
		"APPROACH_CONTROL_TOKEN",
		"never spool",
	})
}

func TestApproachFlowSkillMatchesImplementedFlowCLIContract(t *testing.T) {
	root := repoRoot(t)
	skill := canonicalizeApproachInvocations(readFile(t, filepath.Join(root, "agent-skills", "approach-flow", "SKILL.md")))
	flowCLI := collapseSpaces(readFile(t, filepath.Join(root, "cmd", "approach", "flow.go")))
	planCLI := readFile(t, filepath.Join(root, "cmd", "approach", "plan.go"))
	flowStore := readFile(t, filepath.Join(root, "flowstore", "store.go"))

	if !strings.Contains(skill, "approach flow phase set") || !strings.Contains(flowCLI, "runFlowPhaseSet") {
		t.Fatal("skill and CLI should both expose flow phase set")
	}
	if !strings.Contains(skill, "approach flow phase complete") || !strings.Contains(flowCLI, `command: "complete"`) {
		t.Fatal("skill and CLI should both expose flow phase complete")
	}
	if !strings.Contains(skill, "approach flow phase block") || !strings.Contains(flowCLI, `command: "block"`) {
		t.Fatal("skill and CLI should both expose flow phase block")
	}
	if !strings.Contains(skill, "approach flow phase needs-attention") || !strings.Contains(flowCLI, `command: "needs-attention"`) {
		t.Fatal("skill and CLI should both expose flow phase needs-attention")
	}
	if !strings.Contains(skill, "approach flow phase restart") || !strings.Contains(flowCLI, "runFlowPhaseRestart") {
		t.Fatal("skill and CLI should both expose flow phase restart")
	}
	if !strings.Contains(skill, "approach flow phase reset") || !strings.Contains(flowCLI, "runFlowPhaseReset") {
		t.Fatal("skill and CLI should both expose flow phase reset")
	}
	if !strings.Contains(skill, "approach flow plan set") || !strings.Contains(flowCLI, "runFlowPlanSet") {
		t.Fatal("skill and CLI should both expose flow plan set")
	}
	if !strings.Contains(skill, "approach flow issue set") || !strings.Contains(flowCLI, "runFlowIssueSet") {
		t.Fatal("skill and CLI should both expose flow issue set")
	}
	if !strings.Contains(skill, "approach flow pr set") || !strings.Contains(flowCLI, "runFlowPRSet") {
		t.Fatal("skill and CLI should both expose flow pr set")
	}
	if !strings.Contains(skill, "approach flow merge set") || !strings.Contains(flowCLI, "runFlowMergeSet") {
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
		"approach flow session attach",
		"approach flow abandon",
	} {
		if hasRunnableCommandExample(skill, unimplementedCommand) {
			t.Fatalf("skill includes a runnable example for unimplemented command %q", unimplementedCommand)
		}
	}
}

func TestApproachFlowCreateSkillDocumentsAgentContract(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow-create", "SKILL.md"))

	requireContainsAll(t, "skill metadata", skill, []string{
		"name: approach-flow-create",
		"make a flow from this",
		"add this as an Approach flow",
		"create an Approach flow for this plan",
	})
	requireContainsAll(t, "independent flow creation", skill, []string{
		"does not require `APPROACH_FLOW_ID` or `APPROACH_FLOW_PHASE_ID`",
		"ask the user",
		"absolute",
	})
	requireContainsAll(t, "shared artifact root setup", skill, []string{
		"APPROACH_ARTIFACT_ROOT",
		"APPROACH_FLOW_STATE_ROOT",
		"APPROACH_PLAN_STATE_ROOT",
		"APPROACH_SESSION_STATE_ROOT",
		"FLOW_STATE_ARGS",
		"PLAN_STATE_ARGS",
	})
	requireContainsAll(t, "flow creation commands", skill, []string{
		"approach flow create",
		"--json",
		"--instructions-file",
		"--instructions",
		"approach flow read --flow-id",
	})
	requireContainsAll(t, "plan import commands", skill, []string{
		"approach plan save",
		"approach flow plan set",
		"approach plan read",
		"approach flow phase complete",
		"Imported plan",
	})
	requireContainsAll(t, "failure handling", skill, []string{
		"persistence failures",
		"must not be treated as success",
		"report the command error",
		"approach flow phase block",
		"approach flow phase needs-attention",
	})

	if hasRunnableCommandExample(skill, "approach flow session attach") {
		t.Fatal("skill includes a runnable example for unimplemented command \"approach flow session attach\"")
	}
	if strings.Contains(skill, "FLOW_PRESET") && regexp.MustCompile(`(?s)approach flow phase (?:block|complete|needs-attention).*?--phase-id plan`).MatchString(skill) {
		t.Fatal("approach-flow-create should use the created Flow's plan-kind phase ID instead of hardcoded --phase-id plan")
	}
}

func TestApproachFlowCreateSkillGuardsPersistenceFailures(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow-create", "SKILL.md"))
	createBlock := fencedBashBlockContaining(t, skill, "FLOW_JSON=$(approach flow create")
	importBlock := fencedBashBlockContaining(t, skill, "PLAN_ID=$(printf")

	requireContainsAll(t, "flow creation persistence guards", skill, []string{
		"if ! FLOW_JSON=$(approach flow create",
		"if ! FLOW_ID=$(printf '%s' \"$FLOW_JSON\" | python3",
		"if ! approach flow read --flow-id \"$FLOW_ID\"",
		"case \"${APPROACH_REPO_PATH:-}\" in",
		"absolute APPROACH_REPO_PATH",
		"exit 1",
	})
	assertGuardedFailureBodies(t, createBlock, "if ! FLOW_JSON=$(approach flow create", 2, []string{
		"approach flow create failed",
		"exit 1",
	})
	assertGuardedFailureBodies(t, createBlock, "if ! FLOW_ID=$(printf", 1, []string{
		"flow_id",
		"exit 1",
	})
	assertGuardedFailureBodies(t, createBlock, `if ! approach flow read --flow-id "$FLOW_ID"`, 1, []string{
		"approach flow read failed",
		"exit 1",
	})

	requireContainsAll(t, "plan import persistence guards", skill, []string{
		"record_plan_import_failure",
		"if ! PLAN_ID=$(printf '%s' \"${PLAN_MARKDOWN:-}\" | approach plan save",
		"if ! approach flow plan set",
		"if ! approach plan read --plan-id \"$PLAN_ID\"",
		"if ! approach flow phase complete",
		"approach flow phase block failed after plan import failure",
		"exit 1",
	})
	for _, guard := range []string{
		"if ! PLAN_ID=$(printf",
		"if ! approach flow plan set",
		`if ! approach plan read --plan-id "$PLAN_ID"`,
		"if ! approach flow phase complete",
	} {
		assertGuardedFailureBodies(t, importBlock, guard, 1, []string{
			"record_plan_import_failure",
			"exit 1",
		})
	}
	if strings.Contains(importBlock, "ready or candidates") {
		t.Fatal("plan import should leave FLOW_PLAN_PHASE_ID empty when no plan-kind phase is ready")
	}

	canonical := canonicalizeApproachInvocations(skill)
	readbackIndex := strings.Index(canonical, `if ! approach plan read --plan-id "$PLAN_ID"`)
	completeIndex := strings.Index(canonical, "if ! approach flow phase complete")
	if readbackIndex < 0 || completeIndex < 0 || completeIndex < readbackIndex {
		t.Fatal("skill should guard plan readback before attempting to complete the Flow plan phase")
	}
}

func TestApproachFlowCreateSkillExamplesAreUnsetSafe(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow-create", "SKILL.md"))

	requireContainsAll(t, "unset-safe shell variables", skill, []string{
		"${FLOW_TITLE:-}",
		"${FLOW_INSTRUCTIONS_FILE:-}",
		"${FLOW_INSTRUCTIONS:-}",
		"${APPROACH_REPO_PATH:-}",
		"${APPROACH_WORKTREE_PATH:-}",
		"${APPROACH_BRANCH:-}",
		"${APPROACH_COMMIT:-}",
		"${FLOW_ID:-}",
		"${PLAN_MARKDOWN:-}",
	})
}

func TestApproachFlowCreateSkillMatchesImplementedCLIContract(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow-create", "SKILL.md"))
	flowCLI := collapseSpaces(readFile(t, filepath.Join(root, "cmd", "approach", "flow.go")))
	planCLI := readFile(t, filepath.Join(root, "cmd", "approach", "plan.go"))

	requireContainsAll(t, "flow CLI contract", flowCLI, []string{
		"runFlowCreate",
		"runFlowRead",
		"runFlowPlanSet",
		`command: "complete"`,
		`command: "block"`,
		`command: "needs-attention"`,
	})
	requireContainsAll(t, "plan CLI contract", planCLI, []string{
		"runPlanSave",
		"runPlanRead",
	})

	assertRunnableExampleFlagsExist(t, skill, flowCLI, "flow")
	assertRunnableExampleFlagsExist(t, skill, planCLI, "plan")
}

func TestApproachFlowSkillKeepsPlanAndFlowStateRootsTogether(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow", "SKILL.md"))

	assertSkillKeepsPlanAndFlowStateRootsTogether(t, skill)
}

func TestApproachFlowCreateSkillKeepsPlanAndFlowStateRootsTogether(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow-create", "SKILL.md"))

	assertSkillKeepsPlanAndFlowStateRootsTogether(t, skill)
}

func assertSkillKeepsPlanAndFlowStateRootsTogether(t *testing.T, skill string) {
	t.Helper()
	requireContainsAll(t, "shared artifact root setup", skill, []string{
		"APPROACH_ARTIFACT_ROOT",
		"APPROACH_FLOW_STATE_ROOT",
		"APPROACH_PLAN_STATE_ROOT",
		"APPROACH_SESSION_STATE_ROOT",
		"FLOW_STATE_ARGS",
		"PLAN_STATE_ARGS",
	})

	for _, block := range fencedBashBlocks(skill) {
		canonical := canonicalizeApproachInvocations(block)
		if strings.Contains(canonical, "approach flow ") && !strings.Contains(block, `"${FLOW_STATE_ARGS[@]}"`) {
			t.Fatalf("flow example missing FLOW_STATE_ARGS:\n%s", block)
		}
		if strings.Contains(canonical, "approach plan ") && !strings.Contains(block, `"${PLAN_STATE_ARGS[@]}"`) {
			t.Fatalf("plan example missing PLAN_STATE_ARGS:\n%s", block)
		}
	}
}

func TestApproachFlowSkillPlanPhaseGuardsPersistenceFailures(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow", "SKILL.md"))

	requireContainsAll(t, "plan persistence guards", skill, []string{
		"if ! PLAN_ID=$(",
		"approach flow plan set",
		`--plan-id "$PLAN_ID"`,
		`--outcome "plan_link_failed"`,
		`--outcome "plan_save_failed"`,
		`--outcome "plan_phase_save_failed"`,
		`--outcome "plan_read_failed"`,
		"exit 1",
	})
}

func TestApproachFlowSkillHandlesMissingPlanID(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow", "SKILL.md"))

	requireContainsAll(t, "missing plan id guidance", skill, []string{
		`if [ -z "$APPROACH_PLAN_ID" ]`,
		`if ! approach plan read --plan-id "$APPROACH_PLAN_ID" "${PLAN_STATE_ARGS[@]}"`,
		`--status blocked`,
		`--outcome "blocked"`,
		`approach plan read --plan-id "$APPROACH_PLAN_ID" "${PLAN_STATE_ARGS[@]}"`,
	})
}

func TestApproachFlowSkillDocumentsPlanReviewGateOutcomes(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, "agent-skills", "approach-flow", "SKILL.md"))

	requireContainsAll(t, "plan review outcome contract", skill, []string{
		"approved",
		"approved_with_concerns",
		"changes_requested",
		"blocked",
		"Approach derives all phase readiness",
		`approach flow phase needs-attention --notes "..."`,
		`approach flow phase complete --outcome "approved_with_concerns" --notes "..."`,
		`approach flow phase block --notes "..."`,
	})
}

func TestApproachFlowInstallationDocs(t *testing.T) {
	root := repoRoot(t)
	readme := readFile(t, filepath.Join(root, "README.md"))
	configDocs := readFile(t, filepath.Join(root, "docs", "config.md"))

	requireContainsAll(t, "README installation docs", readme, []string{
		"agent-skills/approach-flow/",
		"agent-skills/approach-flow-create/",
		"approach-flow",
		"approach-flow-create",
		"approach-plan-persist",
		"symlink",
	})
	requireContainsAll(t, "config installation docs", configDocs, []string{
		"agent-skills/approach-flow/",
		"agent-skills/approach-flow-create/",
		"approach-flow",
		"approach-flow-create",
		"approach-plan-persist",
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
	haystack = canonicalizeApproachInvocations(haystack)
	normalized := strings.Join(strings.Fields(haystack), " ")
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) && !strings.Contains(normalized, needle) {
			t.Fatalf("%s missing %q", label, needle)
		}
	}
}

// canonicalizeApproachInvocations rewrites the pinned spelling a skill uses —
// `"${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}" flow read`, or the bare
// `$APPROACH_BIN flow read` that prose still uses — back to `approach flow read`
// so every matcher in this file keeps expressing one grammar.
//
// This normalization is load-bearing, not cosmetic. The literals below, the
// state-root scan, and runnableApproachSubcommands all key on the token
// `approach`. Without it, rewriting the skills to the pinned form would make
// every one of them match nothing and pass *vacuously* — silently disabling the
// state-root and flag-validation guards, which is worse than a red test.
func canonicalizeApproachInvocations(text string) string {
	return approachBinInvocation.ReplaceAllString(text, "approach ")
}

// The self-healing form is matched first; `|` in Go regexp is leftmost-first at
// a given start position, so listing the bare form ahead of it would strip
// `$APPROACH_BIN` out of the middle of the longer expansion and leave the rest
// of the braces behind as garbage.
var approachBinInvocation = regexp.MustCompile(
	`"\$\{APPROACH_EXECUTABLE:-\$\{APPROACH_BIN:-approach\}\}"\s+|"\$APPROACH_BIN"\s+|\$APPROACH_BIN\s+`)

func hasRunnableCommandExample(markdown, command string) bool {
	for _, block := range fencedBashBlocks(markdown) {
		block = canonicalizeApproachInvocations(block)
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
		if strings.Contains(canonicalizeApproachInvocations(block), text) {
			return canonicalizeApproachInvocations(block)
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
	uses := runnableCommandFlagUses(markdown, command)
	// A zero count is the failure mode this assertion exists for: if the skill's
	// invocation grammar drifts away from what runnableApproachSubcommands
	// recognizes, every check below silently stops running and the test passes
	// having validated nothing.
	if len(uses) == 0 {
		t.Fatalf("no runnable approach %s examples were discovered; the invocation grammar has drifted", command)
	}
	for _, use := range uses {
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
	return "approach " + strings.Join(append([]string{u.TopCommand}, u.Subcommands...), " ")
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

			trimmed = canonicalizeApproachInvocations(trimmed)
			if subcommands, ok := runnableApproachSubcommands(trimmed, command); ok {
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

func hasRunnableApproachCommand(line, command string) bool {
	_, ok := runnableApproachSubcommands(line, command)
	return ok
}

func runnableApproachSubcommands(line, command string) ([]string, bool) {
	// The pinned spelling is canonicalized first, so the prefix and field checks
	// below stay written against the single token `approach`. Both of the
	// checks matter independently: `"$APPROACH_BIN" flow read` would fail the
	// prefix test (the text before the match is `"`, not empty) and the
	// fields[0] test (strings.Fields yields `"$APPROACH_BIN"`).
	line = canonicalizeApproachInvocations(line)
	pattern := "approach " + command + " "
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
	if len(fields) < 3 || fields[0] != "approach" || fields[1] != command {
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
		"flow phase reset":           "runFlowPhaseReset",
		"flow phase add-child":       "runFlowPhaseAddChild",
		"flow plan set":              "runFlowPlanSet",
		"flow issue set":             "runFlowIssueSet",
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

// A pin that was supplied and is unusable must stop the workflow, not degrade to
// PATH. Degrading there runs whatever build happens to be installed against a
// database the launcher owns, which is the mixed-schema incident the pin exists
// to prevent — so the skills report it as a persistence failure and exit. Only a
// session that never received a pin uses PATH, which is why the test is on
// APPROACH_EXECUTABLE and not on the resolved APPROACH_BIN.
func TestBundledSkillsRefuseAnUnusablePinInsteadOfFallingBackToPath(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"approach-flow", "approach-flow-create", "approach-plan-persist"} {
		t.Run(name, func(t *testing.T) {
			skill := readFile(t, filepath.Join(root, "agent-skills", name, "SKILL.md"))
			block := fencedBashBlockContaining(t, skill, `APPROACH_BIN="${APPROACH_EXECUTABLE:-approach}"`)
			if !strings.Contains(block, `[ ! -x "$APPROACH_EXECUTABLE" ]`) {
				t.Fatalf("%s never tests whether the supplied pin is runnable:\n%s", name, block)
			}
			if strings.Contains(block, "unset APPROACH_EXECUTABLE") {
				t.Fatalf("%s unsets an unusable pin and falls back to approach on PATH:\n%s", name, block)
			}
			if !strings.Contains(block, "exit 1") {
				t.Fatalf("%s does not stop on an unusable pin:\n%s", name, block)
			}
		})
	}
}

// collapseSpaces makes struct-literal assertions independent of gofmt's
// column alignment, which shifts whenever a neighbouring field is renamed.
func collapseSpaces(text string) string {
	return spaceRuns.ReplaceAllString(text, " ")
}

var spaceRuns = regexp.MustCompile(` {2,}`)
