package model

import (
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/controlplane"
)

// launchContextVariantRecord is the fixture every variant row derives its
// target from. It is deliberately a plain literal rather than the generic
// agent's harness record: the builder is being pinned at its own interface,
// so the row must not inherit whatever the prepare stage's fixture happens to
// set.
func launchContextVariantRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktree",
		Branch:       "flow/one",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/plan.md",
		Status:       flowstore.StatusInProgress,
		UpdatedAt:    time.Now(),
	}
}

func launchContextVariantSettings() flowLaunchAgentSettingsSnapshot {
	return flowLaunchAgentSettingsSnapshot{
		Command:          "codex",
		Model:            "gpt-5",
		ReasoningEffort:  "high",
		SessionStateRoot: "/state",
	}
}

// TestNewFlowLaunchContextPinsEachVariantsCanonicalContext compares the whole
// returned struct, not a subset of fields, so a field added to any Flow role's
// launch context has to be declared in this table before it can ship.
func TestNewFlowLaunchContextPinsEachVariantsCanonicalContext(t *testing.T) {
	record := launchContextVariantRecord()
	repair, repairRecord := launchContextRepairTarget(t)
	for _, variant := range []struct {
		name   string
		target flowLaunchTarget
		want   actions.AgentLaunchContext
		route  flowLaunchRoute
	}{
		{
			name: "worktree agent",
			target: worktreeAgentTarget{
				LaunchID: "launch-1",
				Record:   record,
				PlanPath: record.PlanPath,
			},
			want: actions.AgentLaunchContext{
				Command:          "codex",
				LaunchID:         "launch-1",
				RepoPath:         record.RepoPath,
				WorktreePath:     record.WorktreePath,
				WorkingDir:       record.WorktreePath,
				Branch:           record.Branch,
				Commit:           record.Commit,
				SessionStateRoot: "/state",
				PlanID:           record.PlanID,
				PlanPath:         record.PlanPath,
				FlowID:           record.FlowID,
				FlowAgent:        true,
				Embedded:         true,
				Model:            "gpt-5",
				ReasoningEffort:  "high",
			},
			route: flowLaunchRouteEmbedded,
		},
		{
			// Repair sets no FlowPhaseID and leaves FlowLaunchTracked false. That
			// empty phase ID is load-bearing: it is what makes
			// flowLaunchFailureUpdate refuse, which is what keeps a failed repair
			// from mutating a phase. WorkingDir stays empty too — unlike the
			// worktree agent, repair does not chdir into a directory it cannot
			// assume still exists.
			name:   "repair",
			target: repair,
			want: actions.AgentLaunchContext{
				// Command, Model and effort come from the obstruction phase's
				// resolved settings on the target, not from the TUI snapshot.
				Command:          "claude",
				LaunchID:         "launch-1",
				RepoPath:         repairRecord.RepoPath,
				WorktreePath:     repairRecord.WorktreePath,
				Branch:           repairRecord.Branch,
				Commit:           repairRecord.Commit,
				SessionStateRoot: "/state",
				PlanID:           repairRecord.PlanID,
				PlanPath:         repairRecord.PlanPath,
				FlowID:           repairRecord.FlowID,
				FlowRepair:       true,
				Embedded:         true,
				Headless:         true,
				Model:            "opus",
				ReasoningEffort:  "medium",
				InitialPrompt:    launchContextRepairPrompt(repairRecord, ""),
			},
			route: flowLaunchRouteEmbedded,
		},
	} {
		t.Run(variant.name, func(t *testing.T) {
			ctx, route, err := newFlowLaunchContext(variant.target, launchContextVariantSettings())
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx != variant.want {
				t.Fatalf("context = %#v, want %#v", ctx, variant.want)
			}
			if route != variant.route {
				t.Fatalf("route = %v, want %v", route, variant.route)
			}
		})
	}
}

func TestNewFlowLaunchContextStampsThePinnedBuild(t *testing.T) {
	claims := stubRetainLaunchPin(t)
	settings := launchContextVariantSettings()
	settings.Pin = controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
		Digest:         "abc123def456",
	}

	ctx, _, err := newFlowLaunchContext(worktreeAgentTarget{
		LaunchID: "launch-1",
		Record:   launchContextVariantRecord(),
	}, settings)
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if ctx.Executable != settings.Pin.ExecutablePath || ctx.BuildVersion != "v0.10.3" || ctx.DBSchemaVersion != 6 {
		t.Fatalf("builder did not stamp the pin: %#v", ctx)
	}
	if len(*claims) != 1 || (*claims)[0] != [3]string{"/state", "launch-1", "abc123def456"} {
		t.Fatalf("pin claims = %v, want exactly one for this launch", *claims)
	}
}

func TestNewFlowLaunchContextRejectsIncompletePayloads(t *testing.T) {
	for _, missing := range []struct {
		name   string
		target worktreeAgentTarget
	}{
		{
			name:   "launch id",
			target: worktreeAgentTarget{Record: launchContextVariantRecord()},
		},
		{
			name: "flow id",
			target: func() worktreeAgentTarget {
				record := launchContextVariantRecord()
				record.FlowID = "  "
				return worktreeAgentTarget{LaunchID: "launch-1", Record: record}
			}(),
		},
		{
			name: "worktree path",
			target: func() worktreeAgentTarget {
				record := launchContextVariantRecord()
				record.WorktreePath = ""
				return worktreeAgentTarget{LaunchID: "launch-1", Record: record}
			}(),
		},
	} {
		t.Run("missing "+missing.name, func(t *testing.T) {
			ctx, route, err := newFlowLaunchContext(missing.target, launchContextVariantSettings())
			if err == nil {
				t.Fatalf("incomplete payload built a context: %#v", ctx)
			}
			if route != 0 {
				t.Fatalf("failed build returned route %v", route)
			}
		})
	}
}

// launchContextRepairTarget is the repair fixture. Unlike the worktree agent's
// record it needs real directories: the builder resolves paths through
// flowRepairLaunchPaths, whose ladder is an os.Stat probe, so a fixture built
// from names that merely happen not to exist would pin the fallback branch by
// accident rather than by intent.
func launchContextRepairTarget(t *testing.T) (repairTarget, flowstore.FlowRecord) {
	t.Helper()
	dir := t.TempDir()
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     dir,
		WorktreePath: dir,
		Branch:       "flow/one",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/plan.md",
		Status:       flowstore.StatusInProgress,
		Headless:     true,
		UpdatedAt:    time.Now(),
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Kind:    flowstore.KindImplementation,
			Status:  flowstore.PhaseBlocked,
			Outcome: flowstore.OutcomeBlocked,
			Notes:   "persisted metadata is inconsistent",
			Order:   1,
		}},
	}
	return repairTarget{
		LaunchID:             "launch-1",
		Record:               record,
		Agent:                agent.Settings{Command: "claude", Model: "opus", ReasoningEffort: "medium"},
		FallbackRepoPath:     dir,
		FallbackWorktreePath: dir,
		PlanID:               record.PlanID,
		PlanPath:             record.PlanPath,
	}, record
}

// launchContextRepairPrompt computes the expectation rather than pasting it, so
// an edit to the repair prompt copy does not have to be mirrored here. What the
// row pins is that the builder renders the prompt from this record with this
// binary — not the wording.
func launchContextRepairPrompt(record flowstore.FlowRecord, binary string) string {
	obstruction, _ := flowRepairObstructionForRecord(record)
	return flowRepairPrompt(record, obstruction, binary)
}

// TestNewFlowLaunchContextBuildsRepairFromTheRecordsPlanRule pins the rule the
// call site used to own: the record's own plan path always wins, and the read
// stage's resolved path survives only while the record still points at the same
// plan. A record that has moved to another plan must not carry the old plan's
// markdown into the repair session.
func TestNewFlowLaunchContextBuildsRepairFromTheRecordsPlanRule(t *testing.T) {
	for _, tt := range []struct {
		name           string
		recordPlanPath string
		recordPlanID   string
		want           string
	}{
		{name: "record plan path wins", recordPlanPath: "/state/record-plan.md", recordPlanID: "plan-1", want: "/state/record-plan.md"},
		{name: "matching plan id keeps the read stage path", recordPlanPath: "", recordPlanID: "plan-1", want: "/state/read-plan.md"},
		{name: "different plan id drops the read stage path", recordPlanPath: "", recordPlanID: "plan-2", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := launchContextRepairTarget(t)
			target.Record.PlanPath = tt.recordPlanPath
			target.Record.PlanID = tt.recordPlanID
			target.PlanID = "plan-1"
			target.PlanPath = "/state/read-plan.md"

			ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings())
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx.PlanPath != tt.want {
				t.Fatalf("plan path = %q, want %q", ctx.PlanPath, tt.want)
			}
			if ctx.PlanID != tt.recordPlanID {
				t.Fatalf("plan id = %q, want the record's %q", ctx.PlanID, tt.recordPlanID)
			}
		})
	}
}

// TestNewFlowLaunchContextResolvesRepairPathsThroughTheFallbackLadder covers the
// argument order the builder has to transcribe correctly: recorded worktree,
// then recorded repo, then the read stage's worktree, then its repo — and, when
// nothing is usable, the read stage's pair kept verbatim so repair can still
// reach a Flow whose directories are all gone.
func TestNewFlowLaunchContextResolvesRepairPathsThroughTheFallbackLadder(t *testing.T) {
	fallbackWorktree := t.TempDir()
	fallbackRepo := t.TempDir()
	for _, tt := range []struct {
		name             string
		recordRepo       string
		recordWorktree   string
		fallbackWorktree string
		fallbackRepo     string
		wantRepo         string
		wantWorktree     string
	}{
		{
			name:             "unusable record falls back to the read stage worktree",
			recordRepo:       "/dev/null/missing-repo",
			recordWorktree:   "/dev/null/missing-worktree",
			fallbackWorktree: fallbackWorktree,
			fallbackRepo:     fallbackRepo,
			wantRepo:         fallbackWorktree,
			wantWorktree:     fallbackWorktree,
		},
		{
			name:             "no read stage worktree falls back to its repo",
			recordRepo:       "/dev/null/missing-repo",
			recordWorktree:   "/dev/null/missing-worktree",
			fallbackWorktree: "/dev/null/missing-fallback",
			fallbackRepo:     fallbackRepo,
			wantRepo:         fallbackRepo,
			wantWorktree:     fallbackRepo,
		},
		{
			name:             "nothing usable keeps the read stage pair",
			recordRepo:       "/dev/null/missing-repo",
			recordWorktree:   "/dev/null/missing-worktree",
			fallbackWorktree: "/dev/null/missing-fallback-worktree",
			fallbackRepo:     "/dev/null/missing-fallback-repo",
			wantRepo:         "/dev/null/missing-fallback-repo",
			wantWorktree:     "/dev/null/missing-fallback-worktree",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := launchContextRepairTarget(t)
			target.Record.RepoPath = tt.recordRepo
			target.Record.WorktreePath = tt.recordWorktree
			target.FallbackWorktreePath = tt.fallbackWorktree
			target.FallbackRepoPath = tt.fallbackRepo

			ctx, _, err := newFlowLaunchContext(target, launchContextVariantSettings())
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx.RepoPath != tt.wantRepo || ctx.WorktreePath != tt.wantWorktree {
				t.Fatalf("paths = (%q, %q), want (%q, %q)", ctx.RepoPath, ctx.WorktreePath, tt.wantRepo, tt.wantWorktree)
			}
		})
	}
}

// TestNewFlowLaunchContextRendersTheRepairPromptWithThePinnedBinary is the
// ordering guard. The prompt used to read ctx.Executable, which only had a value
// because the old call site stamped before refreshing; the builder stamps last,
// so it must render from the pin directly. A regression here is silent — the
// prompt would still be well-formed, just telling the agent to run whatever
// `approach` is on PATH instead of the pinned build.
func TestNewFlowLaunchContextRendersTheRepairPromptWithThePinnedBinary(t *testing.T) {
	stubRetainLaunchPin(t)
	target, record := launchContextRepairTarget(t)
	settings := launchContextVariantSettings()
	settings.Pin = controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
		Digest:         "abc123def456",
	}

	ctx, _, err := newFlowLaunchContext(target, settings)
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if !strings.Contains(ctx.InitialPrompt, settings.Pin.ExecutablePath) {
		t.Fatalf("repair prompt does not name the pinned binary:\n%s", ctx.InitialPrompt)
	}
	if ctx.InitialPrompt != launchContextRepairPrompt(record, settings.Pin.ExecutablePath) {
		t.Fatalf("repair prompt = %q, want the pinned-binary rendering", ctx.InitialPrompt)
	}
	if ctx.Executable != settings.Pin.ExecutablePath {
		t.Fatalf("repair context was not stamped: %#v", ctx)
	}
}

// TestNewFlowLaunchContextRejectsIncompleteRepairPayloads pins repair's
// validation as deliberately looser than the worktree agent's: repair exists for
// Flows whose recorded directories are gone, so an empty worktree path is an
// accepted payload here. The no-usable-directory refusal is admission's.
func TestNewFlowLaunchContextRejectsIncompleteRepairPayloads(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*repairTarget)
		wantErr bool
	}{
		{name: "missing launch id", mutate: func(target *repairTarget) { target.LaunchID = "  " }, wantErr: true},
		{name: "missing flow id", mutate: func(target *repairTarget) { target.Record.FlowID = "  " }, wantErr: true},
		{
			name: "empty worktree path is accepted",
			mutate: func(target *repairTarget) {
				target.Record.WorktreePath = ""
				target.FallbackWorktreePath = ""
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, _ := launchContextRepairTarget(t)
			tt.mutate(&target)

			ctx, route, err := newFlowLaunchContext(target, launchContextVariantSettings())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("incomplete payload built a context: %#v", ctx)
				}
				if route != 0 {
					t.Fatalf("failed build returned route %v", route)
				}
				return
			}
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if route != flowLaunchRouteEmbedded {
				t.Fatalf("route = %v, want embedded", route)
			}
		})
	}
}
