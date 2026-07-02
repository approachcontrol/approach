package flowstore

import (
	"fmt"
	"sort"

	"github.com/brian-bell/wtui/internal/artifacts"
)

type phaseGraph struct {
	prereqsByIdx  map[int][]int
	dependents    map[int][]int
	released      map[int]bool
	topo          []int
	orderedIndex  map[int]int
	duplicateRows map[int]bool
}

func validatePhaseGraph(phases []FlowPhase) error {
	if err := validateDeclaredPhaseDependencies(phases); err != nil {
		return err
	}
	graph := buildPhaseGraph(phases)
	topLevelIDs := make(map[string]string)
	childIDs := make(map[string]string)
	for _, phase := range phases {
		id := artifacts.NormalizePhaseID(phase.PhaseID)
		if id == "" {
			continue
		}
		if phase.ParentPhaseID == "" {
			if existing := topLevelIDs[id]; existing != "" {
				return fmt.Errorf("duplicate phase id %q", phase.PhaseID)
			}
			topLevelIDs[id] = phase.PhaseID
			continue
		}
		childIDs[id] = phase.PhaseID
	}
	for idx, phase := range phases {
		if phase.ParentPhaseID != "" {
			continue
		}
		for _, dep := range phase.DependsOn {
			depID := artifacts.NormalizePhaseID(dep)
			if depID == "" {
				return fmt.Errorf("phase %q has empty dependency", phase.PhaseID)
			}
			if depID == artifacts.NormalizePhaseID(phase.PhaseID) {
				return fmt.Errorf("phase %q depends on itself", phase.PhaseID)
			}
			if childID := childIDs[depID]; childID != "" {
				return fmt.Errorf("phase %q depends on child phase %q", phase.PhaseID, childID)
			}
			if topLevelIDs[depID] == "" {
				return fmt.Errorf("phase %q: unknown dependency %q", phase.PhaseID, dep)
			}
		}
		if graph.duplicateRows[idx] {
			return fmt.Errorf("duplicate phase id %q", phase.PhaseID)
		}
	}
	if len(graph.topo) != len(phases) {
		return fmt.Errorf("phase graph contains a cycle: %s", cycleDescription(phases, graph))
	}
	topLevelCount := 0
	rootCount := 0
	for _, phase := range phases {
		if phase.ParentPhaseID != "" {
			continue
		}
		topLevelCount++
		if len(phase.DependsOn) == 0 {
			rootCount++
		}
	}
	if topLevelCount > 0 && rootCount == 0 {
		return fmt.Errorf("phase graph must include at least one root phase")
	}
	return nil
}

func validateDeclaredPhaseDependencies(phases []FlowPhase) error {
	for _, phase := range phases {
		if phase.ParentPhaseID != "" && len(phase.DependsOn) > 0 {
			return fmt.Errorf("child phase %q cannot declare dependencies", phase.PhaseID)
		}
		if phase.ParentPhaseID != "" {
			continue
		}
		seen := make(map[string]bool)
		for _, dep := range phase.DependsOn {
			normalized := artifacts.NormalizePhaseID(dep)
			if normalized == "" {
				return fmt.Errorf("phase %q has empty dependency", phase.PhaseID)
			}
			if seen[normalized] {
				return fmt.Errorf("phase %q has duplicate dependency %q", phase.PhaseID, dep)
			}
			seen[normalized] = true
		}
	}
	return nil
}

func buildPhaseGraph(phases []FlowPhase) phaseGraph {
	ordered := OrderedPhases(phases)
	orderedIndex := make(map[int]int, len(phases))
	used := make([]bool, len(phases))
	for orderedPos, orderedPhase := range ordered {
		for i, phase := range phases {
			if used[i] || !samePhaseRow(phase, orderedPhase) {
				continue
			}
			used[i] = true
			orderedIndex[i] = orderedPos
			break
		}
	}
	firstTopLevelByID := make(map[string]int)
	firstAnyByID := make(map[string]int)
	duplicateRows := make(map[int]bool)
	for i, phase := range phases {
		id := artifacts.NormalizePhaseID(phase.PhaseID)
		if id == "" {
			continue
		}
		if _, ok := firstAnyByID[id]; ok {
			duplicateRows[i] = true
		} else {
			firstAnyByID[id] = i
		}
		if phase.ParentPhaseID != "" {
			continue
		}
		if _, ok := firstTopLevelByID[id]; ok {
			duplicateRows[i] = true
			continue
		}
		firstTopLevelByID[id] = i
	}

	childrenByParent := make(map[string][]int)
	for i, phase := range phases {
		if phase.ParentPhaseID == "" {
			continue
		}
		parentID := artifacts.NormalizePhaseID(phase.ParentPhaseID)
		childrenByParent[parentID] = append(childrenByParent[parentID], i)
	}
	for parentID := range childrenByParent {
		sort.SliceStable(childrenByParent[parentID], func(i, j int) bool {
			left := phases[childrenByParent[parentID][i]]
			right := phases[childrenByParent[parentID][j]]
			if left.Order == right.Order {
				return left.PhaseID < right.PhaseID
			}
			return left.Order < right.Order
		})
	}

	prereqSets := make(map[int]map[int]bool, len(phases))
	for i := range phases {
		prereqSets[i] = make(map[int]bool)
	}
	for i, phase := range phases {
		if phase.ParentPhaseID != "" {
			continue
		}
		for _, dep := range phase.DependsOn {
			depIdx, ok := firstTopLevelByID[artifacts.NormalizePhaseID(dep)]
			if !ok || depIdx == i {
				continue
			}
			prereqSets[i][depIdx] = true
			for _, childIdx := range childrenByParent[artifacts.NormalizePhaseID(phases[depIdx].PhaseID)] {
				prereqSets[i][childIdx] = true
			}
		}
	}
	for parentID, childIndexes := range childrenByParent {
		parentIdx, ok := firstTopLevelByID[parentID]
		if !ok {
			continue
		}
		for childPos, childIdx := range childIndexes {
			prereqSets[childIdx][parentIdx] = true
			if childPos > 0 {
				prereqSets[childIdx][childIndexes[childPos-1]] = true
			}
		}
	}

	prereqsByIdx := make(map[int][]int, len(phases))
	dependents := make(map[int][]int, len(phases))
	invalid := make(map[int]bool)
	for i, phase := range phases {
		if duplicateRows[i] {
			invalid[i] = true
		}
		if phase.ParentPhaseID == "" {
			for _, dep := range phase.DependsOn {
				if _, ok := firstTopLevelByID[artifacts.NormalizePhaseID(dep)]; !ok {
					invalid[i] = true
				}
			}
		}
		for prereq := range prereqSets[i] {
			prereqsByIdx[i] = append(prereqsByIdx[i], prereq)
			dependents[prereq] = append(dependents[prereq], i)
		}
	}
	sortByOrderedPosition := func(values []int) {
		sort.SliceStable(values, func(i, j int) bool {
			left := orderedIndex[values[i]]
			right := orderedIndex[values[j]]
			if left == right {
				return values[i] < values[j]
			}
			return left < right
		})
	}
	for i := range prereqsByIdx {
		sortByOrderedPosition(prereqsByIdx[i])
	}
	for i := range dependents {
		sortByOrderedPosition(dependents[i])
	}

	released := make(map[int]bool, len(phases))
	topo := topologicalRelease(phases, prereqsByIdx, dependents, orderedIndex, invalid)
	for _, idx := range topo {
		released[idx] = true
	}
	return phaseGraph{
		prereqsByIdx:  prereqsByIdx,
		dependents:    dependents,
		released:      released,
		topo:          topo,
		orderedIndex:  orderedIndex,
		duplicateRows: duplicateRows,
	}
}

func topologicalRelease(phases []FlowPhase, prereqsByIdx, dependents map[int][]int, orderedIndex map[int]int, invalid map[int]bool) []int {
	invalidClosure := make(map[int]bool, len(invalid))
	queue := make([]int, 0, len(invalid))
	for idx := range invalid {
		invalidClosure[idx] = true
		queue = append(queue, idx)
	}
	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		for _, dependent := range dependents[idx] {
			if invalidClosure[dependent] {
				continue
			}
			invalidClosure[dependent] = true
			queue = append(queue, dependent)
		}
	}

	indegree := make(map[int]int, len(phases))
	for i := range phases {
		if invalidClosure[i] {
			continue
		}
		for _, prereq := range prereqsByIdx[i] {
			if invalidClosure[prereq] {
				continue
			}
			indegree[i]++
		}
	}
	ready := make([]int, 0, len(phases))
	for i := range phases {
		if invalidClosure[i] || indegree[i] != 0 {
			continue
		}
		ready = append(ready, i)
	}
	sort.SliceStable(ready, func(i, j int) bool {
		return orderedIndex[ready[i]] < orderedIndex[ready[j]]
	})
	out := make([]int, 0, len(phases))
	for len(ready) > 0 {
		idx := ready[0]
		ready = ready[1:]
		out = append(out, idx)
		for _, dependent := range dependents[idx] {
			if invalidClosure[dependent] {
				continue
			}
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.SliceStable(ready, func(i, j int) bool {
					return orderedIndex[ready[i]] < orderedIndex[ready[j]]
				})
			}
		}
	}
	return out
}

func samePhaseRow(left, right FlowPhase) bool {
	return left.PhaseID == right.PhaseID &&
		left.ParentPhaseID == right.ParentPhaseID &&
		left.Title == right.Title &&
		left.Order == right.Order &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.UpdatedAt.Equal(right.UpdatedAt)
}

func cycleDescription(phases []FlowPhase, graph phaseGraph) string {
	for i, phase := range phases {
		if graph.released[i] || graph.duplicateRows[i] {
			continue
		}
		for _, prereq := range graph.prereqsByIdx[i] {
			if graph.released[prereq] || graph.duplicateRows[prereq] {
				continue
			}
			return fmt.Sprintf("%s -> %s -> %s", phases[prereq].PhaseID, phase.PhaseID, phases[prereq].PhaseID)
		}
		return phase.PhaseID
	}
	return "unknown"
}

func phaseIDsCompatibleWithDefaultSequence(phases []FlowPhase) bool {
	defaultIDs := []string{"plan", "plan-review", "implementation", "review-loop", "pr-creation", "autoreview", "merge"}
	seen := make(map[string]bool)
	pos := 0
	for _, phase := range OrderedPhases(phases) {
		if phase.ParentPhaseID != "" {
			continue
		}
		id := artifacts.NormalizePhaseID(phase.PhaseID)
		if seen[id] {
			continue
		}
		for pos < len(defaultIDs) && defaultIDs[pos] != id {
			pos++
		}
		if pos == len(defaultIDs) {
			return false
		}
		seen[id] = true
		pos++
	}
	return len(seen) > 0
}

func normalizeDependsOnValues(phases []FlowPhase) []FlowPhase {
	for i := range phases {
		if phases[i].ParentPhaseID != "" {
			phases[i].DependsOn = []string{}
			continue
		}
		values := make([]string, 0, len(phases[i].DependsOn))
		seen := make(map[string]bool)
		for _, dep := range phases[i].DependsOn {
			normalized := artifacts.NormalizePhaseID(dep)
			if normalized == "" || seen[normalized] {
				continue
			}
			seen[normalized] = true
			values = append(values, normalized)
		}
		sort.Strings(values)
		phases[i].DependsOn = values
	}
	return phases
}

func backfillLinearDependsOnForCreate(phases []FlowPhase) []FlowPhase {
	allNil := true
	for _, phase := range phases {
		if phase.ParentPhaseID == "" && phase.DependsOn != nil {
			allNil = false
			break
		}
	}
	if !allNil {
		return normalizeDependsOnValues(phases)
	}
	return backfillLinearDependsOn(phases)
}

type rawDependsOnState struct {
	Present bool
}

func backfillLinearDependsOnFromRaw(phases []FlowPhase, presence []rawDependsOnState) []FlowPhase {
	anyPresent := false
	for i, phase := range phases {
		if phase.ParentPhaseID == "" && i < len(presence) && presence[i].Present {
			anyPresent = true
		}
	}
	if anyPresent {
		return normalizeDependsOnValues(phases)
	}
	if !phaseIDsCompatibleWithDefaultSequence(phases) {
		return phases
	}
	return backfillLinearDependsOn(phases)
}

func backfillLinearDependsOn(phases []FlowPhase) []FlowPhase {
	ordered := OrderedPhases(phases)
	previousByID := make(map[string]string)
	seen := make(map[string]bool)
	var previous string
	for _, phase := range ordered {
		if phase.ParentPhaseID != "" {
			continue
		}
		id := artifacts.NormalizePhaseID(phase.PhaseID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		previousByID[id] = previous
		previous = id
	}
	for i := range phases {
		if phases[i].ParentPhaseID != "" {
			phases[i].DependsOn = []string{}
			continue
		}
		id := artifacts.NormalizePhaseID(phases[i].PhaseID)
		if seen[id] && previousByID[id] != "" && artifacts.NormalizePhaseID(phases[i].PhaseID) == id && !isDuplicateTopLevel(phases, i) {
			phases[i].DependsOn = []string{previousByID[id]}
		} else {
			phases[i].DependsOn = []string{}
		}
	}
	return phases
}

func isDuplicateTopLevel(phases []FlowPhase, idx int) bool {
	id := artifacts.NormalizePhaseID(phases[idx].PhaseID)
	for i := 0; i < idx; i++ {
		if phases[i].ParentPhaseID == "" && artifacts.NormalizePhaseID(phases[i].PhaseID) == id {
			return true
		}
	}
	return false
}

func graphHasAuthoritativeEdges(phases []FlowPhase) bool {
	for _, phase := range phases {
		if phase.ParentPhaseID == "" && phase.DependsOn != nil {
			return true
		}
	}
	return false
}

func hasNonEmptyTopLevelDependsOn(phases []FlowPhase) bool {
	for _, phase := range phases {
		if phase.ParentPhaseID == "" && len(phase.DependsOn) > 0 {
			return true
		}
	}
	return false
}

func allDependencyGatesSatisfied(record FlowRecord, graph phaseGraph, idx int) bool {
	for _, prereq := range graph.prereqsByIdx[idx] {
		if !phaseSatisfiesDownstreamGate(record, record.Phases[prereq]) {
			return false
		}
	}
	return true
}

func phaseDependsOnPlanReviewFailure(record FlowRecord, graph phaseGraph, idx int, failedPlanReview map[int]bool) bool {
	for _, prereq := range graph.prereqsByIdx[idx] {
		phase := record.Phases[prereq]
		if failedPlanReview[prereq] || phase.PhaseID == "plan-review" && !phaseSatisfiesDownstreamGate(record, phase) {
			return true
		}
	}
	return false
}

// PhaseLaunchEligible reports whether the phase at orderedIndex can be offered
// for launch. It is index-aware so stale duplicate rows from hand-authored
// records cannot be launched even when their stored status is ready.
func PhaseLaunchEligible(record FlowRecord, orderedIndex int) bool {
	ordered := OrderedPhases(record.Phases)
	if orderedIndex < 0 || orderedIndex >= len(ordered) {
		return false
	}
	graph := buildPhaseGraph(ordered)
	if !graph.released[orderedIndex] || graph.duplicateRows[orderedIndex] {
		return false
	}
	phase := ordered[orderedIndex]
	if phase.Status != PhaseReady {
		return false
	}
	if !allDependencyGatesSatisfied(FlowRecord{PR: record.PR, Phases: ordered}, graph, orderedIndex) {
		return false
	}
	return artifacts.NormalizePhaseID(phase.PhaseID) != "merge"
}

func phaseLaunchEligibleAtIndex(record FlowRecord, phaseIndex int) bool {
	if phaseIndex < 0 || phaseIndex >= len(record.Phases) {
		return false
	}
	graph := buildPhaseGraph(record.Phases)
	if !graph.released[phaseIndex] || graph.duplicateRows[phaseIndex] {
		return false
	}
	phase := record.Phases[phaseIndex]
	if phase.Status != PhaseReady {
		return false
	}
	if !allDependencyGatesSatisfied(record, graph, phaseIndex) {
		return false
	}
	return artifacts.NormalizePhaseID(phase.PhaseID) != "merge"
}

// FirstLaunchablePhase returns the first ordered phase that can be launched.
func FirstLaunchablePhase(record FlowRecord) (FlowPhase, int, bool) {
	ordered := OrderedPhases(record.Phases)
	for i, phase := range ordered {
		if PhaseLaunchEligible(FlowRecord{Phases: ordered}, i) {
			return phase, i, true
		}
	}
	return FlowPhase{}, -1, false
}
