package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/approachcontrol/approach/planstore"
)

// runPlan handles `approach plan ...` subcommands. It may load config to resolve the
// artifact root but must never scan repositories or start the TUI.
func runPlan(args []string, deps runDeps) error {
	if len(args) == 3 && isHelpArg(args[2]) {
		printPlanHelp(deps.stdout)
		return nil
	}
	if len(args) < 3 {
		return fmt.Errorf("usage: approach plan <save|list|read|status|phase> [flags]")
	}
	switch args[2] {
	case "save":
		return runPlanSave(args[3:], deps)
	case "list":
		return runPlanList(args[3:], deps)
	case "read":
		return runPlanRead(args[3:], deps)
	case "status":
		return runPlanStatus(args[3:], deps)
	case "phase":
		return runPlanPhase(args[3:], deps)
	default:
		return unknownCommandError(args[2], []string{"save", "list", "read", "status", "phase"}, planHelpText)
	}
}

func printPlanHelp(w io.Writer) {
	io.WriteString(w, planHelpText)
}

const planHelpText = `Usage: approach plan <save|list|read|status|phase> [flags]

Persist saved plan artifacts under the approach agent-artifact root.

Commands:
  save       Save or update a Markdown plan; optionally prints JSON.
  list       List saved plans as JSON.
  read       Print a saved plan's Markdown or JSON.
  status set Update only a saved plan's lifecycle status.
  phase set  Create or update a saved-plan phase row.

Examples:
  printf '%s' "$PLAN_MD" | approach plan save --title "Persist plans" --status draft
  approach plan save --plan-id "$PLAN_ID" --title "Persist plans" --file ./plan.md
  approach plan read --plan-id "$PLAN_ID"
  approach plan status set --plan-id "$PLAN_ID" --status in_progress
  approach plan list --repo-path "$REPO" --json
  approach plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store" --status completed --order 1

Most commands accept:
  --state-root PATH  Override the artifact state root after the leaf command.
`

// resolvePlanRoot applies the documented precedence:
// --state-root > APPROACH_FLOW_STATE_ROOT > APPROACH_PLAN_STATE_ROOT >
// APPROACH_SESSION_STATE_ROOT >
// [sessions].root from config > planstore.DefaultRoot() (resolved by NewStore).
func resolvePlanRoot(stateRoot string, deps runDeps) (string, error) {
	if stateRoot != "" {
		return stateRoot, nil
	}
	if root := deps.getenv("APPROACH_FLOW_STATE_ROOT"); root != "" {
		return root, nil
	}
	if root := deps.getenv("APPROACH_PLAN_STATE_ROOT"); root != "" {
		return root, nil
	}
	if root := deps.getenv("APPROACH_SESSION_STATE_ROOT"); root != "" {
		return root, nil
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		return "", fmt.Errorf("error loading config: %w", err)
	}
	return cfg.Sessions.Root, nil
}

func newPlanStore(stateRoot string, deps runDeps) (*planstore.Store, error) {
	root, err := resolvePlanRoot(stateRoot, deps)
	if err != nil {
		return nil, err
	}
	return planstore.NewStore(planstore.StoreOptions{Root: root})
}

type planCommandResult struct {
	planstore.PlanRecord
	PlanPath string `json:"plan_path"`
	Markdown string `json:"markdown,omitempty"`
}

func runPlanSave(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("plan save", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printPlanSaveHelp(deps.stdout) }
	title := flags.String("title", "", "plan title")
	summary := flags.String("summary", "", "plan summary")
	planID := flags.String("plan-id", deps.getenv("APPROACH_PLAN_ID"), "reuse an existing plan id")
	status := flags.String("status", "", "plan status")
	source := flags.String("source", "", "plan source")
	provider := flags.String("provider", "", "agent provider")
	sessionID := flags.String("session-id", "", "provider session id")
	launchID := flags.String("launch-id", "", "approach launch id")
	repoPath := flags.String("repo-path", "", "repository path")
	worktreePath := flags.String("worktree-path", "", "worktree path")
	branch := flags.String("branch", "", "branch name")
	commit := flags.String("commit", "", "commit hash")
	file := flags.String("file", "", "read markdown from file instead of stdin")
	stateRoot := flags.String("state-root", "", "artifact state root")
	asJSON := flags.Bool("json", false, "emit JSON output")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if strings.TrimSpace(*title) == "" {
		return fmt.Errorf("plan save requires --title")
	}

	markdown, err := readPlanInput(*file, deps.stdin)
	if err != nil {
		return err
	}

	store, err := newPlanStore(*stateRoot, deps)
	if err != nil {
		return err
	}

	record := planstore.PlanRecord{
		PlanID:       *planID,
		Title:        *title,
		Summary:      *summary,
		Markdown:     markdown,
		Status:       *status,
		Source:       *source,
		Provider:     fallbackEnv(*provider, "APPROACH_AGENT", deps),
		SessionID:    *sessionID,
		LaunchID:     fallbackEnv(*launchID, "APPROACH_LAUNCH_ID", deps),
		RepoPath:     fallbackEnv(*repoPath, "APPROACH_REPO_PATH", deps),
		WorktreePath: fallbackEnv(*worktreePath, "APPROACH_WORKTREE_PATH", deps),
		Branch:       fallbackEnv(*branch, "APPROACH_BRANCH", deps),
		Commit:       fallbackEnv(*commit, "APPROACH_COMMIT", deps),
	}
	if shouldResolvePlanGitMetadata(store, record) {
		resolvePlanGitMetadata(&record, deps)
	}
	savedID, err := store.Save(record)
	if err != nil {
		return err
	}
	if *asJSON {
		metadata, err := store.ReadMetadata(savedID)
		if err != nil {
			return err
		}
		root, err := resolvePlanRoot(*stateRoot, deps)
		if err != nil {
			return err
		}
		planPath, err := planstore.MarkdownPath(root, savedID)
		if err != nil {
			return err
		}
		data, err := json.Marshal(planCommandResult{PlanRecord: metadata, PlanPath: planPath})
		if err != nil {
			return fmt.Errorf("encode saved plan: %w", err)
		}
		fmt.Fprintln(deps.stdout, string(data))
		return nil
	}
	fmt.Fprintln(deps.stdout, savedID)
	return nil
}

func printPlanSaveHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach plan save [flags]

Save or update a Markdown plan. Markdown is read from stdin unless --file is set.

Required flags:
  --title TITLE

Common flags:
  --plan-id ID       Update an existing plan id.
  --status STATUS    Plan status, such as draft.
  --file PATH        Read Markdown from a file.
  --repo-path PATH   Repository path metadata.
  --json             Print saved plan metadata and plan path as JSON.
  --state-root PATH  Override the artifact state root.

Examples:
  printf '%s' "$PLAN_MD" | approach plan save --title "Persist plans" --status draft
  approach plan save --plan-id "$PLAN_ID" --title "Persist plans" --file ./plan.md
`)
}

func printPlanListHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach plan list [flags]

List saved plans as JSON.

Required flags:
  --json

Common flags:
  --repo-path PATH   Filter by repository path.
  --state-root PATH  Override the artifact state root.

Example:
  approach plan list --repo-path "$REPO" --json
`)
}

func printPlanReadHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach plan read [flags]

Print a saved plan's Markdown.

Required flags:
  --plan-id PLAN_ID

Common flags:
  --json             Print metadata, Markdown, and plan path as JSON.
  --state-root PATH  Override the artifact state root.

Example:
  approach plan read --plan-id "$PLAN_ID"
`)
}

func runPlanList(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("plan list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printPlanListHelp(deps.stdout) }
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
		return fmt.Errorf("plan list requires --json in v1")
	}
	store, err := newPlanStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	records, err := store.List(planstore.PlanFilter{RepoPath: *repoPath})
	if err != nil {
		return err
	}
	if records == nil {
		records = []planstore.PlanRecord{}
	}
	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("encode plan list: %w", err)
	}
	fmt.Fprintln(deps.stdout, string(data))
	return nil
}

func runPlanRead(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("plan read", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printPlanReadHelp(deps.stdout) }
	planID := flags.String("plan-id", deps.getenv("APPROACH_PLAN_ID"), "plan id")
	stateRoot := flags.String("state-root", "", "artifact state root")
	asJSON := flags.Bool("json", false, "emit JSON output")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *planID == "" {
		return fmt.Errorf("plan read requires --plan-id")
	}
	store, err := newPlanStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	markdown, err := store.ReadPlan(*planID)
	if err != nil {
		return err
	}
	if *asJSON {
		metadata, err := store.ReadMetadata(*planID)
		if err != nil {
			return err
		}
		root, err := resolvePlanRoot(*stateRoot, deps)
		if err != nil {
			return err
		}
		planPath, err := planstore.MarkdownPath(root, *planID)
		if err != nil {
			return err
		}
		data, err := json.Marshal(planCommandResult{PlanRecord: metadata, PlanPath: planPath, Markdown: markdown})
		if err != nil {
			return fmt.Errorf("encode plan: %w", err)
		}
		fmt.Fprintln(deps.stdout, string(data))
		return nil
	}
	fmt.Fprint(deps.stdout, markdown)
	return nil
}

func runPlanStatus(args []string, deps runDeps) error {
	if len(args) < 1 || args[0] != "set" {
		return fmt.Errorf("usage: approach plan status set [flags]")
	}
	flags := flag.NewFlagSet("plan status set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printPlanStatusSetHelp(deps.stdout) }
	planID := flags.String("plan-id", deps.getenv("APPROACH_PLAN_ID"), "plan id")
	status := flags.String("status", "", "plan status")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if len(args) == 2 && isHelpArg(args[1]) {
		printPlanStatusSetHelp(deps.stdout)
		return nil
	}
	if help, err := parseCommandFlags(flags, args[1:]); help || err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q\n\n%s", flags.Arg(0), planHelpText)
	}
	if *planID == "" || *status == "" {
		return fmt.Errorf("plan status set requires --plan-id and --status")
	}
	store, err := newPlanStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	record, err := store.SetStatus(*planID, *status)
	if err != nil {
		return err
	}
	root, err := resolvePlanRoot(*stateRoot, deps)
	if err != nil {
		return err
	}
	planPath, err := planstore.MarkdownPath(root, *planID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(planCommandResult{PlanRecord: record, PlanPath: planPath})
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	fmt.Fprintln(deps.stdout, string(data))
	return nil
}

func printPlanStatusSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach plan status set [flags]

Update only a saved plan's lifecycle status. The plan id defaults from
APPROACH_PLAN_ID when launched by Approach.

Required flags:
  --plan-id PLAN_ID
  --status STATUS

Common flags:
  --state-root PATH
`)
}

func runPlanPhase(args []string, deps runDeps) error {
	if len(args) == 1 && isHelpArg(args[0]) {
		printPlanPhaseHelp(deps.stdout)
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: approach plan phase set [flags]")
	}
	if args[0] != "set" {
		return unknownCommandError(args[0], []string{"set"}, planPhaseHelpText)
	}
	flags := flag.NewFlagSet("plan phase set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printPlanPhaseSetHelp(deps.stdout) }
	planID := flags.String("plan-id", deps.getenv("APPROACH_PLAN_ID"), "plan id")
	phaseID := flags.String("phase-id", firstNonEmpty(deps.getenv("APPROACH_PLAN_PHASE_ID"), deps.getenv("APPROACH_FLOW_PHASE_ID")), "phase id")
	title := flags.String("title", "", "phase title")
	status := flags.String("status", "", "phase status")
	order := flags.Int("order", 0, "phase order")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args[1:]); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	if *planID == "" || *phaseID == "" {
		return fmt.Errorf("plan phase set requires --plan-id and --phase-id")
	}
	store, err := newPlanStore(*stateRoot, deps)
	if err != nil {
		return err
	}
	return store.SetPhase(*planID, planstore.PlanPhase{
		PhaseID: *phaseID,
		Title:   *title,
		Status:  *status,
		Order:   *order,
	})
}

func printPlanPhaseHelp(w io.Writer) {
	io.WriteString(w, planPhaseHelpText)
}

const planPhaseHelpText = `Usage: approach plan phase set [flags]

Update a saved-plan phase row.

Example:
  approach plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store" --status completed --order 1
`

func printPlanPhaseSetHelp(w io.Writer) {
	io.WriteString(w, `Usage: approach plan phase set [flags]

Create or update a saved-plan phase row.

Required flags:
  --plan-id PLAN_ID
  --phase-id PHASE_ID

Common flags:
  --title TITLE
  --status STATUS
  --order N
  --state-root PATH

Example:
  approach plan phase set --plan-id "$PLAN_ID" --phase-id store --title "Store" --status completed --order 1
`)
}

func readPlanInput(file string, stdin io.Reader) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read plan file: %w", err)
		}
		return string(data), nil
	}
	if stdin == nil {
		return "", nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read plan from stdin: %w", err)
	}
	return string(data), nil
}

func fallbackEnv(value, key string, deps runDeps) string {
	if value != "" {
		return value
	}
	return deps.getenv(key)
}

type planGitMetadata struct {
	RepoPath     string
	WorktreePath string
	Branch       string
	Commit       string
	Linked       bool
}

func shouldResolvePlanGitMetadata(store *planstore.Store, record planstore.PlanRecord) bool {
	if record.PlanID == "" {
		return true
	}
	hasIncomingLocation := record.RepoPath != "" || record.WorktreePath != ""
	return hasIncomingLocation || !store.HasPlan(record.PlanID)
}

func resolvePlanGitMetadata(record *planstore.PlanRecord, deps runDeps) {
	cwd, err := deps.getwd()
	if err != nil {
		cwd = ""
	}
	candidate := record.WorktreePath
	if candidate == "" {
		candidate = record.RepoPath
	}
	if candidate == "" {
		candidate = cwd
	}
	if meta, ok := resolvePlanGitMetadataAt(candidate); ok {
		applyPlanGitMetadata(record, meta)
	}
}

func applyPlanGitMetadata(record *planstore.PlanRecord, meta planGitMetadata) {
	if record.RepoPath == "" {
		record.RepoPath = meta.RepoPath
	} else if meta.Linked && meta.RepoPath != "" && samePlanPath(record.RepoPath, meta.WorktreePath) {
		record.RepoPath = meta.RepoPath
	}
	if record.WorktreePath == "" {
		record.WorktreePath = meta.WorktreePath
	}
	if record.Branch == "" {
		record.Branch = meta.Branch
	}
	if record.Commit == "" {
		record.Commit = meta.Commit
	}
}

func samePlanPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func resolvePlanGitMetadataAt(cwd string) (planGitMetadata, bool) {
	if cwd == "" {
		return planGitMetadata{}, false
	}
	worktreePath, _ := planGitOutput(cwd, "rev-parse", "--show-toplevel")
	commonDir, _ := planGitOutput(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	gitDir, _ := planGitOutput(cwd, "rev-parse", "--path-format=absolute", "--git-dir")
	if worktreePath == "" && commonDir == "" && gitDir == "" {
		return planGitMetadata{}, false
	}
	isBare := false
	if out, err := planGitOutput(cwd, "rev-parse", "--is-bare-repository"); err == nil {
		isBare = out == "true"
	}
	commonDirIsBare := false
	if commonDir != "" {
		if out, err := planGitOutput(commonDir, "rev-parse", "--is-bare-repository"); err == nil {
			commonDirIsBare = out == "true"
		}
	}
	linked := planIsLinkedWorktreeGitDir(gitDir, commonDir)
	repoPath := planRepoPathFromGitMetadata(worktreePath, gitDir, commonDir, isBare, commonDirIsBare)
	branch, _ := planGitOutput(cwd, "branch", "--show-current")
	commit, _ := planGitOutput(cwd, "rev-parse", "HEAD")
	return planGitMetadata{
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Branch:       branch,
		Commit:       commit,
		Linked:       linked,
	}, true
}

func planRepoPathFromGitMetadata(worktreePath, gitDir, commonDir string, isBare, commonDirIsBare bool) string {
	if isBare {
		if commonDir != "" {
			return filepath.Clean(commonDir)
		}
		if gitDir == "" {
			return ""
		}
		return filepath.Clean(gitDir)
	}
	if commonDir != "" && gitDir != "" && planIsLinkedWorktreeGitDir(gitDir, commonDir) {
		if commonDirIsBare {
			return filepath.Clean(commonDir)
		}
		if filepath.Base(filepath.Clean(commonDir)) != ".git" {
			return worktreePath
		}
		return planRepoPathFromGitCommonDir(commonDir)
	}
	if worktreePath != "" {
		return worktreePath
	}
	if commonDir != "" {
		return planRepoPathFromGitCommonDir(commonDir)
	}
	if gitDir == "" {
		return ""
	}
	return planRepoPathFromGitCommonDir(gitDir)
}

func planIsLinkedWorktreeGitDir(gitDir, commonDir string) bool {
	if gitDir == "" || commonDir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Join(filepath.Clean(commonDir), "worktrees"), filepath.Clean(gitDir))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func planRepoPathFromGitCommonDir(commonDir string) string {
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return commonDir
}

func planGitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
