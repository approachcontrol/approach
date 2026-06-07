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

	"github.com/brian-bell/wtui/planstore"
)

// runPlan handles `wtui plan ...` subcommands. It may load config to resolve the
// artifact root but must never scan repositories or start the TUI.
func runPlan(args []string, deps runDeps) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: wtui plan <save|list|read|phase> [flags]")
	}
	switch args[2] {
	case "save":
		return runPlanSave(args[3:], deps)
	case "list":
		return runPlanList(args[3:], deps)
	case "read":
		return runPlanRead(args[3:], deps)
	case "phase":
		return runPlanPhase(args[3:], deps)
	default:
		return fmt.Errorf("unknown plan subcommand %q", args[2])
	}
}

// resolvePlanRoot applies the documented precedence:
// --state-root > WTUI_PLAN_STATE_ROOT > WTUI_SESSION_STATE_ROOT >
// [sessions].root from config > planstore.DefaultRoot() (resolved by NewStore).
func resolvePlanRoot(stateRoot string, deps runDeps) (string, error) {
	if stateRoot != "" {
		return stateRoot, nil
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

func newPlanStore(stateRoot string, deps runDeps) (*planstore.Store, error) {
	root, err := resolvePlanRoot(stateRoot, deps)
	if err != nil {
		return nil, err
	}
	return planstore.NewStore(planstore.StoreOptions{Root: root})
}

func runPlanSave(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("plan save", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	title := flags.String("title", "", "plan title")
	summary := flags.String("summary", "", "plan summary")
	planID := flags.String("plan-id", "", "reuse an existing plan id")
	status := flags.String("status", "", "plan status")
	source := flags.String("source", "", "plan source")
	provider := flags.String("provider", "", "agent provider")
	sessionID := flags.String("session-id", "", "provider session id")
	launchID := flags.String("launch-id", "", "wtui launch id")
	repoPath := flags.String("repo-path", "", "repository path")
	worktreePath := flags.String("worktree-path", "", "worktree path")
	branch := flags.String("branch", "", "branch name")
	commit := flags.String("commit", "", "commit hash")
	file := flags.String("file", "", "read markdown from file instead of stdin")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if err := flags.Parse(args); err != nil {
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
		Provider:     fallbackEnv(*provider, "WTUI_AGENT", deps),
		SessionID:    *sessionID,
		LaunchID:     fallbackEnv(*launchID, "WTUI_LAUNCH_ID", deps),
		RepoPath:     fallbackEnv(*repoPath, "WTUI_REPO_PATH", deps),
		WorktreePath: fallbackEnv(*worktreePath, "WTUI_WORKTREE_PATH", deps),
		Branch:       fallbackEnv(*branch, "WTUI_BRANCH", deps),
		Commit:       fallbackEnv(*commit, "WTUI_COMMIT", deps),
	}
	if shouldResolvePlanGitMetadata(store, record) {
		resolvePlanGitMetadata(&record, deps)
	}
	savedID, err := store.Save(record)
	if err != nil {
		return err
	}
	fmt.Fprintln(deps.stdout, savedID)
	return nil
}

func runPlanList(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("plan list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repoPath := flags.String("repo-path", "", "filter by repository path")
	stateRoot := flags.String("state-root", "", "artifact state root")
	asJSON := flags.Bool("json", false, "emit JSON output")
	if err := flags.Parse(args); err != nil {
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
	planID := flags.String("plan-id", "", "plan id")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if err := flags.Parse(args); err != nil {
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
	fmt.Fprint(deps.stdout, markdown)
	return nil
}

func runPlanPhase(args []string, deps runDeps) error {
	if len(args) < 1 || args[0] != "set" {
		return fmt.Errorf("usage: wtui plan phase set [flags]")
	}
	flags := flag.NewFlagSet("plan phase set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	planID := flags.String("plan-id", "", "plan id")
	phaseID := flags.String("phase-id", "", "phase id")
	title := flags.String("title", "", "phase title")
	status := flags.String("status", "", "phase status")
	order := flags.Int("order", 0, "phase order")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if err := flags.Parse(args[1:]); err != nil {
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
	hasIncomingMetadata := record.RepoPath != "" || record.WorktreePath != "" || record.Branch != "" || record.Commit != ""
	return hasIncomingMetadata || !store.HasPlan(record.PlanID)
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
