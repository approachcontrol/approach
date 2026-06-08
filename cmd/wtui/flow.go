package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brian-bell/wtui/flowstore"
)

// runFlow handles `wtui flow ...` subcommands. It may load config to resolve
// the artifact root but must never scan repositories or start the TUI.
func runFlow(args []string, deps runDeps) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: wtui flow <create|list|read|phase|plan> [flags]")
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
	default:
		return fmt.Errorf("unknown flow subcommand %q", args[2])
	}
}

// resolveFlowRoot applies the documented precedence:
// --state-root > WTUI_FLOW_STATE_ROOT > WTUI_PLAN_STATE_ROOT >
// WTUI_SESSION_STATE_ROOT > [sessions].root from config > flowstore.DefaultRoot().
func resolveFlowRoot(stateRoot string, deps runDeps) (string, error) {
	if stateRoot != "" {
		return stateRoot, nil
	}
	if root := deps.getenv("WTUI_FLOW_STATE_ROOT"); root != "" {
		return root, nil
	}
	if root := deps.getenv("WTUI_PLAN_STATE_ROOT"); root != "" {
		return root, nil
	}
	if root := deps.getenv("WTUI_SESSION_STATE_ROOT"); root != "" {
		return root, nil
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		return "", fmt.Errorf("error loading config: %w", err)
	}
	return cfg.Sessions.Root, nil
}

func newFlowStore(stateRoot string, deps runDeps) (*flowstore.Store, error) {
	root, err := resolveFlowRoot(stateRoot, deps)
	if err != nil {
		return nil, err
	}
	return flowstore.NewStore(flowstore.StoreOptions{Root: root})
}

func runFlowCreate(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	title := flags.String("title", "", "flow title")
	instructions := flags.String("instructions", "", "task instructions")
	instructionsFile := flags.String("instructions-file", "", "read task instructions from file")
	repoPath := flags.String("repo-path", "", "repository path")
	worktreePath := flags.String("worktree-path", "", "worktree path")
	branch := flags.String("branch", "", "branch name")
	baseRef := flags.String("base-ref", "", "base ref")
	commit := flags.String("commit", "", "start commit")
	stateRoot := flags.String("state-root", "", "artifact state root")
	asJSON := flags.Bool("json", false, "emit JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*asJSON {
		return fmt.Errorf("flow create requires --json in v1")
	}
	if strings.TrimSpace(*title) == "" {
		return fmt.Errorf("flow create requires --title")
	}
	body, err := readFlowInstructions(*instructions, *instructionsFile)
	if err != nil {
		return err
	}
	store, err := newFlowStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        *title,
		Instructions: body,
		RepoPath:     *repoPath,
		WorktreePath: *worktreePath,
		Branch:       *branch,
		BaseRef:      *baseRef,
		Commit:       *commit,
	})
	if err != nil {
		return err
	}
	return writeFlowJSON(deps.stdout, record)
}

func runFlowList(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repoPath := flags.String("repo-path", "", "filter by repository path")
	stateRoot := flags.String("state-root", "", "artifact state root")
	asJSON := flags.Bool("json", false, "emit JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*asJSON {
		return fmt.Errorf("flow list requires --json in v1")
	}
	store, err := newFlowStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	records, err := store.List(flowstore.FlowFilter{RepoPath: *repoPath})
	if err != nil {
		return err
	}
	if records == nil {
		records = []flowstore.FlowRecord{}
	}
	return writeFlowJSON(deps.stdout, records)
}

func runFlowRead(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow read", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flowID := flags.String("flow-id", "", "flow id")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow read requires --flow-id")
	}
	store, err := newFlowStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	record, err := store.Read(*flowID)
	if err != nil {
		return err
	}
	return writeFlowJSON(deps.stdout, record)
}

func runFlowPhase(args []string, deps runDeps) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: wtui flow phase <set|add-child> [flags]")
	}
	switch args[0] {
	case "set":
		return runFlowPhaseSet(args[1:], deps)
	case "add-child":
		return runFlowPhaseAddChild(args[1:], deps)
	default:
		return fmt.Errorf("usage: wtui flow phase <set|add-child> [flags]")
	}
}

func runFlowPhaseSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow phase set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flowID := flags.String("flow-id", "", "flow id")
	phaseID := flags.String("phase-id", "", "phase id")
	status := flags.String("status", "", "phase status")
	outcome := flags.String("outcome", "", "phase outcome")
	summary := flags.String("summary", "", "phase summary")
	notes := flags.String("notes", "", "phase notes")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if err := flags.Parse(args); err != nil {
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
	switch *status {
	case flowstore.PhaseRunning, flowstore.PhaseCompleted, flowstore.PhaseNeedsAttention, flowstore.PhaseBlocked, flowstore.PhaseSkipped:
	case flowstore.PhaseReady:
		return fmt.Errorf("cannot set phase status to ready; readiness is derived")
	default:
		return fmt.Errorf("unsupported agent-facing phase status %q", *status)
	}
	store, err := newFlowStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	record, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  *flowID,
		PhaseID: *phaseID,
		Status:  *status,
		Outcome: *outcome,
		Notes:   *notes,
		Summary: *summary,
	})
	if err != nil {
		return err
	}
	return writeFlowJSON(deps.stdout, record)
}

func runFlowPhaseAddChild(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow phase add-child", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flowID := flags.String("flow-id", "", "flow id")
	parentPhaseID := flags.String("parent-phase-id", "implementation", "parent phase id")
	phaseID := flags.String("phase-id", "", "child phase id")
	title := flags.String("title", "", "child phase title")
	order := flags.Int("order", 0, "child phase order under implementation")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if err := flags.Parse(args); err != nil {
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
	store, err := newFlowStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	record, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
		FlowID:        *flowID,
		ParentPhaseID: *parentPhaseID,
		PhaseID:       *phaseID,
		Title:         *title,
		Order:         *order,
	})
	if err != nil {
		return err
	}
	return writeFlowJSON(deps.stdout, record)
}

func runFlowPlan(args []string, deps runDeps) error {
	if len(args) < 1 || args[0] != "set" {
		return fmt.Errorf("usage: wtui flow plan set [flags]")
	}
	return runFlowPlanSet(args[1:], deps)
}

func runFlowPlanSet(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("flow plan set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flowID := flags.String("flow-id", "", "flow id")
	planID := flags.String("plan-id", "", "plan id")
	planPath := flags.String("plan-path", "", "plan markdown path")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *flowID == "" {
		return fmt.Errorf("flow plan set requires --flow-id")
	}
	if *planID == "" {
		return fmt.Errorf("flow plan set requires --plan-id")
	}
	store, err := newFlowStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	record, err := store.SetPlanLink(flowstore.PlanLinkUpdate{
		FlowID:   *flowID,
		PlanID:   *planID,
		PlanPath: *planPath,
	})
	if err != nil {
		return err
	}
	return writeFlowJSON(deps.stdout, record)
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

func writeFlowJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode flow JSON: %w", err)
	}
	fmt.Fprintln(w, string(data))
	return nil
}
