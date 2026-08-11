package model

// flowLaunchKind is the closed set of launch intents the lifecycle can be asked
// to run. Every kind is declared now so later beads add implementations rather
// than constants; only flowLaunchKindManualPhase is implemented today.
type flowLaunchKind int

const (
	flowLaunchKindManualPhase flowLaunchKind = iota + 1
	flowLaunchKindCreatePhase
	flowLaunchKindAutoPhase
	flowLaunchKindPhaseResume
	flowLaunchKindRepair
	flowLaunchKindWorktreeAgent
	flowLaunchKindSavedSessionResume
)

// flowLaunchRoute selects the handoff the prepared context takes.
type flowLaunchRoute int

const (
	flowLaunchRouteExternal flowLaunchRoute = iota
	flowLaunchRouteEmbedded
)

// flowLaunchIntent is what a caller submits. It carries only what the caller
// knows: everything else — agent settings, prompt templates, phase, headless
// preference — the lifecycle reads from the Model or the authoritative record.
// The resume, create, and AutoMode kinds will need per-kind payloads, and
// routing may need the submitting surface; both are added with the code that
// reads them rather than declared unused here.
type flowLaunchIntent struct {
	Kind    flowLaunchKind
	FlowID  string
	PhaseID string
}
