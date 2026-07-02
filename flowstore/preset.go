package flowstore

import (
	"fmt"
	"strings"
	"time"

	"github.com/brian-bell/wtui/internal/artifacts"
)

// PhaseSpec is a data-only phase declaration used by Flow phase graph presets.
type PhaseSpec struct {
	ID        string
	Title     string
	Kind      string
	DependsOn []string
}

// Preset declares a reusable graph of top-level Flow phases.
type Preset struct {
	Name   string
	Phases []PhaseSpec
}

// CreateOptions configures Flow creation without changing the persisted schema.
type CreateOptions struct {
	Preset *Preset
}

// DefaultPreset returns the built-in phase graph used when callers do not
// provide a custom graph.
func DefaultPreset() Preset {
	return Preset{
		Name: "default",
		Phases: []PhaseSpec{
			{ID: "plan", Title: "Plan", Kind: KindPlan, DependsOn: []string{}},
			{ID: "plan-review", Title: "Plan Review", Kind: KindPlanReview, DependsOn: []string{"plan"}},
			{ID: "implementation", Title: "Implementation", Kind: KindImplementation, DependsOn: []string{"plan-review"}},
			{ID: "review-loop", Title: "Review loop", Kind: KindReviewLoop, DependsOn: []string{"implementation"}},
			{ID: "pr-creation", Title: "PR creation", Kind: KindPRCreation, DependsOn: []string{"review-loop"}},
			{ID: "autoreview", Title: "Autoreview", Kind: KindAutoreview, DependsOn: []string{"pr-creation"}},
			{ID: "merge", Title: "Merge", Kind: KindMerge, DependsOn: []string{"autoreview"}},
		},
	}
}

func validatePreset(preset Preset) error {
	if strings.TrimSpace(preset.Name) == "" {
		return fmt.Errorf("preset name is required")
	}
	if len(preset.Phases) == 0 {
		return fmt.Errorf("preset %q must include at least one phase", preset.Name)
	}
	phases := make([]FlowPhase, 0, len(preset.Phases))
	for i, spec := range preset.Phases {
		id := artifacts.NormalizePhaseID(spec.ID)
		if id == "" {
			return fmt.Errorf("phase %d id is required", i+1)
		}
		if err := validatePhaseID(id); err != nil {
			return err
		}
		title := strings.TrimSpace(spec.Title)
		if title == "" {
			return fmt.Errorf("phase %q title is required", id)
		}
		kind := strings.ToLower(strings.TrimSpace(spec.Kind))
		if kind == KindImplementationChild {
			return fmt.Errorf("%s cannot be used in a preset", KindImplementationChild)
		}
		if kind != "" && !knownPresetPhaseKind(kind) {
			return fmt.Errorf("unknown phase kind %q (valid kinds: %s)", spec.Kind, strings.Join(knownPresetPhaseKinds(), ", "))
		}
		phases = append(phases, FlowPhase{
			PhaseID:   id,
			Title:     title,
			Kind:      kind,
			DependsOn: append([]string(nil), spec.DependsOn...),
			Status:    PhasePending,
			Order:     i + 1,
		})
	}
	return validatePhaseGraph(phases)
}

func seedPhases(specs []PhaseSpec, createdAt, updatedAt time.Time) []FlowPhase {
	phases := make([]FlowPhase, 0, len(specs))
	for i, spec := range specs {
		phases = append(phases, FlowPhase{
			PhaseID:   artifacts.NormalizePhaseID(spec.ID),
			Title:     strings.TrimSpace(spec.Title),
			Kind:      strings.ToLower(strings.TrimSpace(spec.Kind)),
			DependsOn: append([]string{}, spec.DependsOn...),
			Status:    PhasePending,
			Order:     i + 1,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}
	return normalizeDependsOnValues(phases)
}

func knownPresetPhaseKind(kind string) bool {
	switch kind {
	case KindPlan, KindPlanReview, KindImplementation, KindReviewLoop, KindPRCreation, KindAutoreview, KindMerge:
		return true
	default:
		return false
	}
}

func knownPresetPhaseKinds() []string {
	return []string{
		KindPlan,
		KindPlanReview,
		KindImplementation,
		KindReviewLoop,
		KindPRCreation,
		KindAutoreview,
		KindMerge,
	}
}
