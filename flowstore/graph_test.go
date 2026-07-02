package flowstore

import (
	"strings"
	"testing"
	"time"
)

func TestValidatePhaseGraph(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	phase := func(id string, dependsOn ...string) FlowPhase {
		return FlowPhase{
			PhaseID:   id,
			Title:     id,
			DependsOn: dependsOn,
			Status:    PhasePending,
			Order:     1,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	child := func(id, parent string) FlowPhase {
		p := phase(id)
		p.ParentPhaseID = parent
		return p
	}
	tests := []struct {
		name   string
		phases []FlowPhase
		want   string
	}{
		{
			name:   "linear chain",
			phases: []FlowPhase{phase("plan"), phase("review", "plan"), phase("ship", "review")},
		},
		{
			name:   "diamond",
			phases: []FlowPhase{phase("root"), phase("a", "root"), phase("b", "root"), phase("join", "a", "b")},
		},
		{
			name:   "self dependency",
			phases: []FlowPhase{phase("root", "root")},
			want:   `phase "root" depends on itself`,
		},
		{
			name:   "cycle",
			phases: []FlowPhase{phase("a", "b"), phase("b", "a")},
			want:   "phase graph contains a cycle",
		},
		{
			name:   "dangling dependency",
			phases: []FlowPhase{phase("publish", "reserch")},
			want:   `phase "publish": unknown dependency "reserch"`,
		},
		{
			name:   "dependency on child",
			phases: []FlowPhase{phase("implementation"), child("api", "implementation"), phase("review", "api")},
			want:   `phase "review" depends on child phase "api"`,
		},
		{
			name:   "duplicate",
			phases: []FlowPhase{phase("Plan"), phase("plan")},
			want:   `duplicate phase id "plan"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePhaseGraph(tc.phases)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validatePhaseGraph() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validatePhaseGraph() error = %v, want %q", err, tc.want)
			}
		})
	}
}
