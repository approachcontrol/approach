package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/launchcontrol"
)

// runFlow handles `approach flow ...` subcommands. It may load config to resolve
// the artifact root but must never scan repositories or start the TUI.
func runFlow(args []string, deps runDeps) error {
	if len(args) == 3 && isHelpArg(args[2]) {
		printFlowHelp(deps.stdout)
		return nil
	}
	if len(args) < 3 {
		return fmt.Errorf("usage: approach flow <create|list|read|phase|plan|issue|pr|merge> [flags]")
	}
	switch args[2] {
	case "create":
		return runFlowCreate(args[3:], deps)
	case "list":
		return runFlowList(args[3:], deps)
	case "read":
		return runFlowRead(args[3:], deps)
	case "phase":
		return runFlowPhase(args[3:], deps)
	case "plan":
		return runFlowPlan(args[3:], deps)
	case "issue":
		return runFlowIssue(args[3:], deps)
	case "pr":
		return runFlowPR(args[3:], deps)
	case "merge":
		return runFlowMerge(args[3:], deps)
	default:
		return unknownCommandError(args[2], []string{"create", "list", "read", "phase", "plan", "issue", "pr", "merge"}, flowHelpText)
	}
}

func printFlowHelp(w io.Writer) {
	io.WriteString(w, flowHelpText)
}

const flowHelpText = `Usage: approach flow <create|list|read|phase|plan|issue|pr|merge> [flags]

Create and update task-centric Flow records under the approach agent-artifact root.

Commands:
  create           Create a Flow; prints JSON when --json is present.
  list             List Flows as JSON.
  read             Print one Flow record as JSON.
  phase complete   Mark a Flow phase completed.
  phase block      Mark a Flow phase blocked.
  phase needs-attention
                   Mark a Flow phase as needing attention.
  phase restart    Restart a blocked or needs-attention phase.
  phase reset      Recover a stale running phase back to ready.
  phase set        Advance a Flow phase with explicit status.
  phase add-child  Add or update an implementation child phase.
  phase agent set  Replace or clear a phase's agent settings stamp.
  plan set         Link a saved plan artifact to a Flow.
  issue set        Record GitHub issue metadata.
  pr set           Record pull request metadata.
  merge set        Record merge metadata.

Examples:
  approach flow create --title "Ship saved plans" --instructions "Build it" --repo-path "$REPO" --json
  approach flow read --flow-id "$FLOW_ID"
  approach flow phase complete --flow-id "$FLOW_ID" --phase-id plan --summary "Saved plan"
  approach flow phase block --flow-id "$FLOW_ID" --phase-id implementation --notes "Waiting on review"
  approach flow phase needs-attention --flow-id "$FLOW_ID" --phase-id plan-review --notes "Revise scope"
  approach flow phase restart --flow-id "$FLOW_ID" --phase-id autoreview
  approach flow phase reset --flow-id "$FLOW_ID" --phase-id implementation
  approach flow phase set --flow-id "$FLOW_ID" --phase-id plan --status completed --summary "Plan saved"
  approach flow phase set --flow-id "$FLOW_ID" --phase-id plan-review --status completed --outcome approved
  approach flow phase agent set --flow-id "$FLOW_ID" --phase-id implementation --agent claude --model claude-opus-5
  approach flow issue set --flow-id "$FLOW_ID" --provider github --number 123 --url "$ISSUE_URL"
  approach flow pr set --flow-id "$FLOW_ID" --provider github --number 155 --url "$PR_URL" --head "$BRANCH" --base main
  approach flow merge set --flow-id "$FLOW_ID" --status merged --commit "$SHA" --merged-at "2026-06-09T12:00:00Z"

Most commands accept:
  --state-root PATH  Override the artifact state root after the leaf command.
`

func newFlowStore(stateRoot string, deps runDeps, role flowstore.Role) (*flowstore.Store, error) {
	cfg, err := deps.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("error loading config: %w", err)
	}
	return newFlowStoreWithConfig(stateRoot, cfg, deps, role)
}

func newFlowStoreWithConfig(stateRoot string, cfg config.Config, deps runDeps, role flowstore.Role) (*flowstore.Store, error) {
	root := stateRoot
	// Explicit means "the operator named this root", which is what decides
	// whether a missing directory is a typo to report or a first run to create.
	// A typo'd environment variable deserves the same answer as a typo'd flag —
	// the launch env sets APPROACH_FLOW_STATE_ROOT, so that is where a wrong
	// root is most likely — and only the config fallback is not explicit.
	explicit := root != ""
	if root == "" {
		if envRoot := deps.getenv("APPROACH_FLOW_STATE_ROOT"); envRoot != "" {
			root, explicit = envRoot, true
		} else if envRoot := deps.getenv("APPROACH_PLAN_STATE_ROOT"); envRoot != "" {
			root, explicit = envRoot, true
		} else if envRoot := deps.getenv("APPROACH_SESSION_STATE_ROOT"); envRoot != "" {
			root, explicit = envRoot, true
		} else {
			root = cfg.Sessions.Root
		}
	}
	if root == "" {
		root = cfg.Sessions.Root
	}
	// The env spelling only, deliberately: the dev-live-migration refusal names
	// both --allow-dev-live-migration and APPROACH_ALLOW_DEV_LIVE_MIGRATION=1,
	// and a refusal an operator cannot act on is worse than no refusal. The flag
	// belongs to the TUI's own flag set; threading it through every `flow`
	// subcommand would add a flag to two dozen usage strings for an
	// acknowledgement that is meant to be rare and deliberate.
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root:                  root,
		RootExplicit:          explicit,
		Role:                  role,
		Presets:               cfg.Flow.Presets,
		AllowDevLiveMigration: truthyEnv(deps.getenv("APPROACH_ALLOW_DEV_LIVE_MIGRATION")),
	})
	if err != nil {
		return nil, err
	}
	// Reported here rather than at each call site, because the checks a reader
	// drops — the 0700 root assertion and the journal_mode=WAL write — are
	// substituted by this channel, not deleted, and a diagnostic nothing prints
	// is a deletion with extra steps. `db migrate` opens its own store and
	// prints these itself, so centralizing here does not double up. stderr, so
	// piping `flow list` stays clean.
	for _, warning := range store.OpenDiagnostics().Warnings {
		fmt.Fprintf(deps.stderr, "approach: %s\n", warning)
	}
	return store, nil
}

func runFlowCreate(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowCreateHelp(deps.stdout) }
	title := flags.String("title", "", "flow title")
	instructions := flags.String("instructions", "", "task instructions")
	instructionsFile := flags.String("instructions-file", "", "read task instructions from file")
	repoPath := flags.String("repo-path", "", "repository path")
	worktreePath := flags.String("worktree-path", "", "worktree path")
	branch := flags.String("branch", "", "branch name")
	baseRef := flags.String("base-ref", "", "base ref")
	commit := flags.String("commit", "", "start commit")
	presetName := flags.String("preset", "", "flow phase graph preset")
	stateRoot := flags.String("state-root", "", "artifact state root")
	asJSON := flags.Bool("json", false, "emit JSON output")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if !*asJSON {
		return fmt.Errorf("flow create requires --json in v1")
	}
	if strings.TrimSpace(*title) == "" {
		return fmt.Errorf("flow create requires --title")
	}
	if strings.TrimSpace(*repoPath) == "" {
		return fmt.Errorf("flow create requires --repo-path")
	}
	if !filepath.IsAbs(*repoPath) {
		return fmt.Errorf("flow create requires absolute --repo-path")
	}
	body, err := readFlowInstructions(*instructions, *instructionsFile)
	if err != nil {
		return err
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}
	preset, err := resolveConfiguredFlowPreset(cfg, *presetName)
	if err != nil {
		return err
	}
	store, err := newFlowStoreWithConfig(*stateRoot, cfg, deps, flowstore.RoleWriter)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	record, err := store.CreateWithOptions(flowstore.FlowRecord{
		Title:        *title,
		Instructions: body,
		RepoPath:     *repoPath,
		WorktreePath: *worktreePath,
		Branch:       *branch,
		BaseRef:      *baseRef,
		Commit:       *commit,
	}, flowstore.CreateOptions{
		Preset:     preset,
		PhaseAgent: flowstore.PhaseAgentSettingsFrom(configuredAgentSettings(cfg)),
	})
	if err != nil {
		return err
	}
	return writeFlowJSON(deps.stdout, record)
}

// configuredAgentSettings resolves the [agent] config into the single
// provider-matched selection stamped onto the Flow's phases.
func configuredAgentSettings(cfg config.Config) agent.Settings {
	return agent.Resolve(agent.Preferences{
		Command:      cfg.Agent.Command,
		CodexModel:   cfg.Agent.CodexModel,
		ClaudeModel:  cfg.Agent.ClaudeModel,
		CursorModel:  cfg.Agent.CursorModel,
		CodexEffort:  cfg.Agent.CodexReasoningEffort,
		ClaudeEffort: cfg.Agent.ClaudeReasoningEffort,
	})
}

func resolveConfiguredFlowPreset(cfg config.Config, requested string) (*flowstore.Preset, error) {
	name := normalizeFlowPresetName(requested)
	if name == "" {
		name = normalizeFlowPresetName(cfg.Flow.Preset)
	}
	if name == "" || name == "default" {
		return nil, nil
	}
	for _, preset := range cfg.Flow.Presets {
		presetName := normalizeFlowPresetName(preset.Name)
		if presetName == name {
			preset.Name = presetName
			return &preset, nil
		}
	}
	return nil, fmt.Errorf("unknown flow preset %q (available presets: %s)", name, strings.Join(availableFlowPresetNames(cfg), ", "))
}

func availableFlowPresetNames(cfg config.Config) []string {
	names := []string{"default"}
	for _, preset := range cfg.Flow.Presets {
		name := normalizeFlowPresetName(preset.Name)
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func normalizeFlowPresetName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func printFlowCreateHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow create [flags]

Create a Flow record. JSON output is required in v1.

Required flags:
  --title TITLE
  --instructions TEXT or --instructions-file PATH
  --repo-path PATH
  --json

Common flags:
  --worktree-path PATH
  --branch BRANCH
  --base-ref REF
  --commit SHA
  --preset NAME
  --state-root PATH

Example:
  approach flow create --title "Ship saved plans" --instructions "Build it" --repo-path "$REPO" --json
`)
}

func runFlowList(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowListHelp(deps.stdout) }
	repoPath := flags.String("repo-path", "", "filter by repository path")
	stateRoot := flags.String("state-root", "", "artifact state root")
	asJSON := flags.Bool("json", false, "emit JSON output")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if !*asJSON {
		return fmt.Errorf("flow list requires --json in v1")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbFlowList, launchcontrol.ListPayload{RepoPath: *repoPath})
	if err != nil {
		return err
	}
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleReader)
}

func printFlowListHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow list [flags]

List Flow records as JSON.

Required flags:
  --json

Common flags:
  --repo-path PATH
  --state-root PATH

Example:
  approach flow list --repo-path "$REPO" --json
`)
}

func runFlowRead(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow read", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowReadHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow read requires --flow-id")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbFlowRead, launchcontrol.ReadPayload{})
	if err != nil {
		return err
	}
	req.FlowID = *flowID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleReader)
}

func printFlowReadHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow read [flags]

Print one Flow record as JSON.

Required flags:
  --flow-id FLOW_ID

Common flags:
  --state-root PATH

Example:
  approach flow read --flow-id "$FLOW_ID"
`)
}

func runFlowPhase(args []string, deps runDeps) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printFlowPhaseHelp(deps.stdout)
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: approach flow phase <set|complete|block|needs-attention|restart|reset|add-child|agent> [flags]")
	}
	switch args[0] {
	case "set":
		return runFlowPhaseSet(args[1:], deps)
	case "complete":
		return runFlowPhaseAction(args[1:], deps, flowPhaseActionSpec{
			command:   "complete",
			verb:      launchcontrol.VerbPhaseComplete,
			printHelp: printFlowPhaseCompleteHelp,
		})
	case "block":
		return runFlowPhaseAction(args[1:], deps, flowPhaseActionSpec{
			command:   "block",
			verb:      launchcontrol.VerbPhaseBlock,
			printHelp: printFlowPhaseBlockHelp,
		})
	case "needs-attention":
		return runFlowPhaseAction(args[1:], deps, flowPhaseActionSpec{
			command:   "needs-attention",
			verb:      launchcontrol.VerbPhaseNeedsAttention,
			printHelp: printFlowPhaseNeedsAttentionHelp,
		})
	case "restart":
		return runFlowPhaseRestart(args[1:], deps)
	case "reset":
		return runFlowPhaseReset(args[1:], deps)
	case "add-child":
		return runFlowPhaseAddChild(args[1:], deps)
	case "agent":
		return runFlowPhaseAgent(args[1:], deps)
	default:
		return unknownCommandError(args[0], []string{"set", "complete", "block", "needs-attention", "restart", "reset", "add-child", "agent"}, flowPhaseHelpText)
	}
}

func printFlowPhaseHelp(w io.Writer) {
	io.WriteString(w, flowPhaseHelpText)
}

const flowPhaseHelpText = `Usage: approach flow phase <set|complete|block|needs-attention|restart|reset|add-child|agent> [flags]

Update Flow phase state. Readiness is derived by approach; agents set running,
completed, needs_attention, blocked, or skipped.

Commands:
  set              Set a phase status, outcome, summary, or notes.
  complete         Mark a phase completed and print the next actionable phase.
  block            Mark a phase blocked.
  needs-attention  Mark a phase as needing attention.
  restart          Restart a blocked or needs-attention phase.
  reset            Recover a stale running phase back to ready.
  add-child        Add or update an implementation child phase.
  agent set        Replace or clear a phase's agent settings stamp.

Examples:
  approach flow phase set --flow-id "$FLOW_ID" --phase-id plan --status completed --summary "Saved plan"
  approach flow phase set --flow-id "$FLOW_ID" --phase-id plan-review --status completed --outcome approved
  approach flow phase complete --flow-id "$FLOW_ID" --phase-id plan --summary "Saved plan"
  approach flow phase block --flow-id "$FLOW_ID" --phase-id implementation --notes "Waiting on review"
  approach flow phase needs-attention --flow-id "$FLOW_ID" --phase-id plan-review --outcome changes_requested --notes "Revise scope"
  approach flow phase restart --flow-id "$FLOW_ID" --phase-id autoreview
  approach flow phase reset --flow-id "$FLOW_ID" --phase-id implementation
  approach flow phase set --flow-id "$FLOW_ID" --phase-id implementation --status blocked --notes "Waiting on review"
  approach flow phase add-child --flow-id "$FLOW_ID" --parent-phase-id implementation --phase-id api --title "API work" --order 1
  approach flow phase agent set --flow-id "$FLOW_ID" --phase-id implementation --agent claude --model claude-opus-5

Common flags:
  --state-root PATH  Override the artifact state root.
`

func runFlowPhaseAgent(args []string, deps runDeps) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printFlowPhaseAgentHelp(deps.stdout)
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: approach flow phase agent set [flags]")
	}
	if args[0] != "set" {
		return unknownCommandError(args[0], []string{"set"}, flowPhaseAgentHelpText)
	}
	return runFlowPhaseAgentSet(args[1:], deps)
}

const flowPhaseAgentHelpText = `Usage: approach flow phase agent set [flags]

Replace or clear one Flow phase's agent, model, and reasoning-effort stamp.
`

func printFlowPhaseAgentHelp(w io.Writer) {
	io.WriteString(w, flowPhaseAgentHelpText)
}

func runFlowPhaseAgentSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow phase agent set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowPhaseAgentSetHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	phaseID := flags.String("phase-id", "", "phase id")
	agentCommand := flags.String("agent", "", "agent command")
	model := flags.String("model", "", "agent model")
	effort := flags.String("reasoning-effort", "", "reasoning effort")
	clear := flags.Bool("clear", false, "clear phase settings")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow phase agent set requires --flow-id")
	}
	if *phaseID == "" {
		return fmt.Errorf("flow phase agent set requires --phase-id")
	}
	provided := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if *clear {
		if provided["agent"] || provided["model"] || provided["reasoning-effort"] {
			return fmt.Errorf("flow phase agent set --clear cannot be combined with --agent, --model, or --reasoning-effort")
		}
	} else if strings.TrimSpace(*agentCommand) == "" {
		return fmt.Errorf("flow phase agent set requires --agent or --clear")
	}
	payload := launchcontrol.AgentSetPayload{Clear: *clear}
	if !*clear {
		payload = launchcontrol.AgentSetPayload{Agent: *agentCommand, Model: *model, ReasoningEffort: *effort}
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPhaseAgentSet, payload)
	if err != nil {
		return err
	}
	req.FlowID, req.PhaseID = *flowID, *phaseID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowPhaseAgentSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase agent set [flags]

Replace the complete agent settings stamp for one Flow phase. Omitted model or
reasoning-effort fields inherit that provider's current global preference. Use
--clear to make every field inherit globally; literal "default" is explicit.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID
  --agent AGENT or --clear

Common flags:
  --model MODEL
  --reasoning-effort EFFORT
  --state-root PATH

Examples:
  approach flow phase agent set --flow-id "$FLOW_ID" --phase-id implementation --agent claude --model claude-opus-5
  approach flow phase agent set --flow-id "$FLOW_ID" --phase-id implementation --clear
`)
}

// flowPhaseActionSpec names one `flow phase <action>` leaf. The status each
// action writes and the outcome it defaults to on review kinds live with the
// verb in launchcontrol, so the proxied and direct paths cannot disagree.
type flowPhaseActionSpec struct {
	command   string
	verb      launchcontrol.Verb
	printHelp func(io.Writer)
}

// flowPhaseActionResult and flowPhaseActionState are the shapes the phase
// action leaves print. They live in launchcontrol so the proxied and direct
// paths print one shape; the aliases keep this package's tests readable.
type (
	flowPhaseActionResult = launchcontrol.PhaseActionResult
	flowPhaseActionState  = launchcontrol.PhaseActionState
)

func runFlowPhaseSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow phase set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowPhaseSetHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	phaseID := flags.String("phase-id", "", "phase id")
	status := flags.String("status", "", "phase status")
	outcome := flags.String("outcome", "", "phase outcome")
	summary := flags.String("summary", "", "phase summary")
	notes := flags.String("notes", "", "phase notes")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow phase set requires --flow-id")
	}
	if *phaseID == "" {
		return fmt.Errorf("flow phase set requires --phase-id")
	}
	if *status == "" {
		return fmt.Errorf("flow phase set requires --status")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPhaseSet, launchcontrol.PhaseSetPayload{
		Status:  *status,
		Outcome: *outcome,
		Summary: *summary,
		Notes:   *notes,
	})
	if err != nil {
		return err
	}
	req.FlowID, req.PhaseID = *flowID, *phaseID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowPhaseSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase set [flags]

Set a Flow phase status, outcome, summary, or notes.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID
  --status STATUS

Common flags:
  --outcome OUTCOME
  --summary TEXT
  --notes TEXT
  --state-root PATH

Examples:
  approach flow phase set --flow-id "$FLOW_ID" --phase-id plan --status completed --summary "Saved plan"
  approach flow phase set --flow-id "$FLOW_ID" --phase-id plan-review --status completed --outcome approved
`)
}

func runFlowPhaseAction(args []string, deps runDeps, spec flowPhaseActionSpec) error {
	flags := flag.NewFlagSet("flow phase "+spec.command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { spec.printHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	phaseID := flags.String("phase-id", "", "phase id")
	outcome := flags.String("outcome", "", "phase outcome")
	summary := flags.String("summary", "", "phase summary")
	notes := flags.String("notes", "", "phase notes")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow phase %s requires --flow-id", spec.command)
	}
	if *phaseID == "" {
		return fmt.Errorf("flow phase %s requires --phase-id", spec.command)
	}
	req, err := launchcontrol.NewRequest(spec.verb, launchcontrol.PhaseActionPayload{
		Outcome: *outcome,
		Summary: *summary,
		Notes:   *notes,
	})
	if err != nil {
		return err
	}
	req.FlowID, req.PhaseID = *flowID, *phaseID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func runFlowPhaseRestart(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow phase restart", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowPhaseRestartHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	phaseID := flags.String("phase-id", "", "phase id")
	notes := flags.String("notes", "", "phase notes")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow phase restart requires --flow-id")
	}
	if *phaseID == "" {
		return fmt.Errorf("flow phase restart requires --phase-id")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPhaseRestart, launchcontrol.PhaseRestartPayload{Notes: *notes})
	if err != nil {
		return err
	}
	req.FlowID, req.PhaseID = *flowID, *phaseID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func runFlowPhaseReset(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow phase reset", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowPhaseResetHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	phaseID := flags.String("phase-id", "", "phase id")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow phase reset requires --flow-id")
	}
	if *phaseID == "" {
		return fmt.Errorf("flow phase reset requires --phase-id")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPhaseReset, launchcontrol.PhaseResetPayload{})
	if err != nil {
		return err
	}
	req.FlowID, req.PhaseID = *flowID, *phaseID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowPhaseCompleteHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase complete [flags]

Mark a Flow phase completed and print the next actionable phase state.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID

Common flags:
  --outcome OUTCOME
  --summary TEXT
  --notes TEXT
  --state-root PATH

Examples:
  approach flow phase complete --flow-id "$FLOW_ID" --phase-id plan --summary "Saved plan"
  approach flow phase complete --flow-id "$FLOW_ID" --phase-id plan-review --outcome approved
`)
}

func printFlowPhaseBlockHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase block [flags]

Mark a Flow phase blocked and print the next actionable phase state.
Notes may be required by phase rules.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID

Common flags:
  --outcome OUTCOME
  --summary TEXT
  --notes TEXT
  --state-root PATH

Examples:
  approach flow phase block --flow-id "$FLOW_ID" --phase-id implementation --notes "Waiting on review"
  approach flow phase block --flow-id "$FLOW_ID" --phase-id plan-review --outcome blocked --notes "Waiting on product"
`)
}

func printFlowPhaseNeedsAttentionHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase needs-attention [flags]

Mark a Flow phase as needing attention and print the next actionable phase state.
Notes may be required by phase rules.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID

Common flags:
  --outcome OUTCOME
  --summary TEXT
  --notes TEXT
  --state-root PATH

Examples:
  approach flow phase needs-attention --flow-id "$FLOW_ID" --phase-id implementation --notes "Tests need revision"
  approach flow phase needs-attention --flow-id "$FLOW_ID" --phase-id plan-review --outcome changes_requested --notes "Revise scope"
`)
}

func printFlowPhaseRestartHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase restart [flags]

Restart a blocked or needs-attention Flow phase as running and print the next
actionable phase state.
When --notes is omitted, approach writes a standard rerun note.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID

Common flags:
  --notes TEXT
  --state-root PATH

Examples:
  approach flow phase restart --flow-id "$FLOW_ID" --phase-id autoreview
  approach flow phase restart --flow-id "$FLOW_ID" --phase-id implementation --notes "Rerunning after fixing review findings."
`)
}

func printFlowPhaseResetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase reset [flags]

Recover a stale running Flow phase back to ready and print the next actionable
phase state. approach derives ready after removing the latest stale launch.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID

Common flags:
  --state-root PATH

Examples:
  approach flow phase reset --flow-id "$FLOW_ID" --phase-id implementation
`)
}

func runFlowPhaseAddChild(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow phase add-child", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowPhaseAddChildHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	parentPhaseID := flags.String("parent-phase-id", "implementation", "parent phase id")
	phaseID := flags.String("phase-id", "", "child phase id")
	title := flags.String("title", "", "child phase title")
	order := flags.Int("order", 0, "child phase order under implementation")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow phase add-child requires --flow-id")
	}
	if *phaseID == "" {
		return fmt.Errorf("flow phase add-child requires --phase-id")
	}
	if strings.TrimSpace(*title) == "" {
		return fmt.Errorf("flow phase add-child requires --title")
	}
	if *order < 1 {
		return fmt.Errorf("flow phase add-child requires positive --order")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPhaseAddChild, launchcontrol.AddChildPayload{
		ParentPhaseID: *parentPhaseID,
		PhaseID:       *phaseID,
		Title:         *title,
		Order:         *order,
	})
	if err != nil {
		return err
	}
	req.FlowID = *flowID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowPhaseAddChildHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow phase add-child [flags]

Add or update an implementation child phase.

Required flags:
  --flow-id FLOW_ID
  --phase-id PHASE_ID
  --title TITLE
  --order N

Common flags:
  --parent-phase-id PHASE_ID
  --state-root PATH

Example:
  approach flow phase add-child --flow-id "$FLOW_ID" --parent-phase-id implementation --phase-id api --title "API work" --order 1
`)
}

func runFlowPlan(args []string, deps runDeps) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printFlowPlanHelp(deps.stdout)
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: approach flow plan set [flags]")
	}
	if args[0] != "set" {
		return unknownCommandError(args[0], []string{"set"}, flowPlanHelpText)
	}
	return runFlowPlanSet(args[1:], deps)
}

func printFlowPlanHelp(w io.Writer) {
	io.WriteString(w, flowPlanHelpText)
}

const flowPlanHelpText = `Usage: approach flow plan set [flags]

Link a saved plan artifact to a Flow.

Example:
  approach flow plan set --flow-id "$FLOW_ID" --plan-id "$PLAN_ID"
`

func runFlowPlanSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow plan set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowPlanSetHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	planID := flags.String("plan-id", "", "plan id")
	planPath := flags.String("plan-path", "", "plan markdown path")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow plan set requires --flow-id")
	}
	if *planID == "" {
		return fmt.Errorf("flow plan set requires --plan-id")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPlanSet, launchcontrol.PlanSetPayload{PlanID: *planID, PlanPath: *planPath})
	if err != nil {
		return err
	}
	req.FlowID = *flowID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowPlanSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow plan set [flags]

Link a saved plan artifact to a Flow.

Required flags:
  --flow-id FLOW_ID
  --plan-id PLAN_ID

Common flags:
  --plan-path PATH
  --state-root PATH

Example:
  approach flow plan set --flow-id "$FLOW_ID" --plan-id "$PLAN_ID"
`)
}

func runFlowIssue(args []string, deps runDeps) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printFlowIssueHelp(deps.stdout)
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: approach flow issue set [flags]")
	}
	if args[0] != "set" {
		return unknownCommandError(args[0], []string{"set"}, flowIssueHelpText)
	}
	return runFlowIssueSet(args[1:], deps)
}

func printFlowIssueHelp(w io.Writer) {
	io.WriteString(w, flowIssueHelpText)
}

const flowIssueHelpText = `Usage: approach flow issue set [flags]

Record GitHub issue metadata for a Flow.

Required flags:
  --flow-id FLOW_ID
  --number N
  --url URL

Common flags:
  --provider PROVIDER
  --state-root PATH

Example:
  approach flow issue set --flow-id "$FLOW_ID" --provider github --number 123 --url "$ISSUE_URL"
`

func runFlowIssueSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow issue set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowIssueSetHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	provider := flags.String("provider", "github", "issue provider")
	number := flags.Int("number", 0, "issue number")
	issueURL := flags.String("url", "", "issue URL")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow issue set requires --flow-id")
	}
	if *number <= 0 {
		return fmt.Errorf("flow issue set requires positive --number")
	}
	if *issueURL == "" {
		return fmt.Errorf("flow issue set requires --url")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbIssueSet, launchcontrol.IssueSetPayload{Provider: *provider, Number: *number, URL: *issueURL})
	if err != nil {
		return err
	}
	req.FlowID = *flowID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowIssueSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow issue set [flags]

Record GitHub issue metadata for a Flow.

Required flags:
  --flow-id FLOW_ID
  --number N
  --url URL

Common flags:
  --provider PROVIDER
  --state-root PATH

Example:
  approach flow issue set --flow-id "$FLOW_ID" --provider github --number 123 --url "$ISSUE_URL"
`)
}

func runFlowPR(args []string, deps runDeps) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printFlowPRHelp(deps.stdout)
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: approach flow pr set [flags]")
	}
	if args[0] != "set" {
		return unknownCommandError(args[0], []string{"set"}, flowPRHelpText)
	}
	return runFlowPRSet(args[1:], deps)
}

func printFlowPRHelp(w io.Writer) {
	io.WriteString(w, flowPRHelpText)
}

const flowPRHelpText = `Usage: approach flow pr set [flags]

Record pull request metadata for a Flow.

Example:
  approach flow pr set --flow-id "$FLOW_ID" --provider github --number 155 --url "$PR_URL" --head "$BRANCH" --base main
`

func runFlowPRSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow pr set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowPRSetHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	provider := flags.String("provider", "github", "PR provider")
	number := flags.Int("number", 0, "PR number")
	prURL := flags.String("url", "", "PR URL")
	head := flags.String("head", "", "PR head branch")
	base := flags.String("base", "", "PR base branch")
	status := flags.String("status", "", "PR status")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow pr set requires --flow-id")
	}
	if *number <= 0 {
		return fmt.Errorf("flow pr set requires positive --number")
	}
	if *prURL == "" {
		return fmt.Errorf("flow pr set requires --url")
	}
	if *head == "" {
		return fmt.Errorf("flow pr set requires --head")
	}
	if *base == "" {
		return fmt.Errorf("flow pr set requires --base")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPRSet, launchcontrol.PRSetPayload{
		Provider: *provider,
		Number:   *number,
		URL:      *prURL,
		Head:     *head,
		Base:     *base,
		Status:   *status,
	})
	if err != nil {
		return err
	}
	req.FlowID = *flowID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowPRSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow pr set [flags]

Record pull request metadata for a Flow.

Required flags:
  --flow-id FLOW_ID
  --number N
  --url URL
  --head BRANCH
  --base BRANCH

Common flags:
  --provider PROVIDER
  --status STATUS
  --state-root PATH

Example:
  approach flow pr set --flow-id "$FLOW_ID" --provider github --number 155 --url "$PR_URL" --head "$BRANCH" --base main
`)
}

func runFlowMerge(args []string, deps runDeps) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printFlowMergeHelp(deps.stdout)
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: approach flow merge set [flags]")
	}
	if args[0] != "set" {
		return unknownCommandError(args[0], []string{"set"}, flowMergeHelpText)
	}
	return runFlowMergeSet(args[1:], deps)
}

func printFlowMergeHelp(w io.Writer) {
	io.WriteString(w, flowMergeHelpText)
}

const flowMergeHelpText = `Usage: approach flow merge set [flags]

Record merge metadata for a Flow.

Example:
  approach flow merge set --flow-id "$FLOW_ID" --status merged --commit "$SHA" --merged-at "2026-06-09T12:00:00Z"
`

func runFlowMergeSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow merge set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printFlowMergeSetHelp(deps.stdout) }
	flowID := flags.String("flow-id", "", "flow id")
	status := flags.String("status", "", "merge status")
	commit := flags.String("commit", "", "merge commit")
	mergedAt := flags.String("merged-at", "", "merge timestamp")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow merge set requires --flow-id")
	}
	if *status == "" {
		return fmt.Errorf("flow merge set requires --status")
	}
	req, err := launchcontrol.NewRequest(launchcontrol.VerbMergeSet, launchcontrol.MergeSetPayload{
		Status:   *status,
		Commit:   *commit,
		MergedAt: *mergedAt,
	})
	if err != nil {
		return err
	}
	req.FlowID = *flowID
	return runFlowLeaf(deps, *stateRoot, req, flowstore.RoleWriter)
}

func printFlowMergeSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach flow merge set [flags]

Record merge metadata for a Flow.

Required flags:
  --flow-id FLOW_ID
  --status STATUS

Merged status also requires:
  --commit SHA
  --merged-at RFC3339_TIMESTAMP

Common flags:
  --state-root PATH

Example:
  approach flow merge set --flow-id "$FLOW_ID" --status merged --commit "$SHA" --merged-at "2026-06-09T12:00:00Z"
`)
}

func readFlowInstructions(inline, file string) (string, error) {
	if inline != "" && file != "" {
		return "", fmt.Errorf("flow create accepts either --instructions or --instructions-file, not both")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read flow instructions file: %w", err)
		}
		return string(data), nil
	}
	return inline, nil
}

// runFlowLeaf is every `flow` leaf's tail: validate the request without a
// store, run it, and print the result. Validation first, so an agent-facing
// mistake is refused before any database is opened, exactly as before.
func runFlowLeaf(deps runDeps, stateRoot string, req launchcontrol.Request, role flowstore.Role) error {
	if err := launchcontrol.Validate(req); err != nil {
		return err
	}
	resp, err := runFlowRequest(deps, stateRoot, req, role)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	if err := writeFlowJSON(deps.stdout, json.RawMessage(resp.Result)); err != nil {
		return err
	}
	if resp.Warning != "" {
		if _, writeErr := fmt.Fprintln(deps.stderr, resp.Warning); writeErr != nil {
			return fmt.Errorf("write partial flow list diagnostic: %w", writeErr)
		}
	}
	return nil
}

// runFlowRequest opens the store with role and runs req through the shared
// executor.
func runFlowRequest(deps runDeps, stateRoot string, req launchcontrol.Request, role flowstore.Role) (launchcontrol.Response, error) {
	store, err := newFlowStore(stateRoot, deps, role)
	if err != nil {
		return launchcontrol.Response{}, err
	}
	defer func() { _ = store.Close() }()
	return launchcontrol.Execute(store, req)
}

func writeFlowJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode flow JSON: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(data)); err != nil {
		return fmt.Errorf("write flow JSON: %w", err)
	}
	return nil
}
