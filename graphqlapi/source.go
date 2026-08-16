// Package graphqlapi serves a read-only GraphQL view of Approach's discovered
// repositories and persisted Flow records.
//
// The package owns three things: the per-request read model (source.go), the
// schema and its resolvers (schema.go), and the HTTP handler with its
// transport hardening (server.go, limits.go). It never mutates Flow state —
// no Mutation or Subscription root type exists, and the handler rejects any
// operation that is not a query.
package graphqlapi

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
)

// RepoSource returns the repositories discovered under the configured scan
// root. A failure is non-fatal: the snapshot degrades to an empty scanned set
// and still serves flow-derived repos (a missing or mistyped scan root is the
// ordinary case, not an exceptional one).
type RepoSource func() ([]scanner.Repo, error)

// FlowSource returns every persisted Flow record, in the store's order
// (UpdatedAt descending). A typed partial-list error accompanies usable healthy
// records and is logged server-side; every other failure surfaces to clients as
// a sanitized GraphQL error.
type FlowSource func() ([]flowstore.FlowRecord, error)

// EpicProgressionSource reads one persisted epic auto-progression row by its
// canonical key, distinguishing a missing row (found=false) from an unreadable
// one (a non-nil error). It is a read seam onto flowstore only: the API never
// enables, disables, or halts progression, and it never reaches Beads itself.
type EpicProgressionSource func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error)

// errStateUnavailable is the fixed message clients see when the primary data
// source fails. Underlying paths and os error text are logged server-side and
// never reach a response body.
var errStateUnavailable = errors.New("internal error reading application state")

// Repo is one repository in a snapshot. Entries come either from the scanner
// or from a Flow record that references a repo outside the scan root.
type Repo struct {
	Path        string
	DisplayName string
	IsBare      bool
	InScanRoot  bool
}

// Flow pairs a persisted record with its normalized repo path, so repo
// linkage and the Repo.id it resolves against always agree.
type Flow struct {
	Record   flowstore.FlowRecord
	RepoPath string
}

// epicProgressionProjection is one request's single read of one canonical key:
// either the row that was there, or the fact that there was none. Holding both
// in the snapshot is what keeps sibling Flows sharing an epic — found or
// missing — from re-reading the store per resolver.
type epicProgressionProjection struct {
	record flowstore.EpicProgression
	found  bool
}

// snapshot is the immutable read model for exactly one request: one scan plus
// one store list, indexed for pure map and slice reads by every resolver.
type snapshot struct {
	repos        []*Repo
	repoByPath   map[string]*Repo
	flows        []*Flow
	flowByID     map[string]*Flow
	flowsByRepo  map[string][]*Flow
	progressions map[flowstore.EpicProgressionKey]epicProgressionProjection
	err          error
}

// buildSnapshot reads every source once and indexes the result. logf receives
// the unsanitized failure detail; it is never nil-checked by callers below, so
// buildSnapshot substitutes a no-op.
func buildSnapshot(repos RepoSource, flows FlowSource, progressions EpicProgressionSource, logf func(string, ...any)) *snapshot {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	snap := &snapshot{
		repoByPath:   make(map[string]*Repo),
		flowByID:     make(map[string]*Flow),
		flowsByRepo:  make(map[string][]*Flow),
		progressions: make(map[flowstore.EpicProgressionKey]epicProgressionProjection),
	}

	var scanned []scanner.Repo
	if repos != nil {
		var err error
		scanned, err = repos()
		if err != nil {
			// Degrade rather than fail: a pure flows query must not break
			// because the scan root is missing.
			scanned = nil
			logf("repository scan failed: %v", err)
		}
	}
	for _, repo := range scanned {
		path, ok := normalizePath(repo.Path)
		if !ok {
			continue
		}
		if _, exists := snap.repoByPath[path]; exists {
			continue
		}
		snap.addRepo(&Repo{
			Path:        path,
			DisplayName: repo.DisplayName,
			IsBare:      repo.IsBare,
			InScanRoot:  true,
		})
	}

	if flows == nil {
		sortRepos(snap.repos)
		return snap
	}
	records, err := flows()
	if err != nil {
		if _, partial := flowstore.AsPartialList(err); partial {
			logf("reading flow records partially degraded: %v", err)
		} else {
			logf("reading flow records failed: %v", err)
			snap.err = errStateUnavailable
			return snap
		}
	}
	for _, record := range records {
		// An externally written record with an empty repo_path would
		// otherwise synthesize a phantom repo at the server's working
		// directory, and Flow.repo is non-null. Skip the record entirely.
		path, ok := normalizePath(record.RepoPath)
		if !ok {
			continue
		}
		flow := &Flow{Record: record, RepoPath: path}
		snap.flows = append(snap.flows, flow)
		// Keyed the same way Flow(id) looks up, so a padded persisted id
		// cannot become permanently unreachable.
		flowID := strings.TrimSpace(record.FlowID)
		if _, exists := snap.flowByID[flowID]; !exists {
			snap.flowByID[flowID] = flow
		}
		if _, exists := snap.repoByPath[path]; !exists {
			snap.addRepo(&Repo{
				Path:        path,
				DisplayName: filepath.Base(path),
				InScanRoot:  false,
			})
		}
		snap.flowsByRepo[path] = append(snap.flowsByRepo[path], flow)
	}
	sortRepos(snap.repos)
	if progressions != nil {
		snap.loadEpicProgressions(progressions, logf)
	}
	return snap
}

// loadEpicProgressions reads one row per distinct canonical key referenced by a
// loaded Flow, so the resolvers below do no I/O at all.
//
// The key is built exactly the way flowstore.ReadEpicProgression canonicalizes
// one — the snapshot's normalized absolute repo path plus the trimmed epic id —
// so two Flows that name the same epic through different spellings collapse to
// a single read, and a Flow with no epic link causes none.
func (s *snapshot) loadEpicProgressions(read EpicProgressionSource, logf func(string, ...any)) {
	for _, flow := range s.flows {
		key, ok := epicProgressionKey(flow)
		if !ok {
			continue
		}
		if _, loaded := s.progressions[key]; loaded {
			continue
		}
		record, found, err := read(key)
		if err != nil {
			// The key names a real path on this machine and the cause may name
			// the store file, so both stay server-side; clients get the same
			// fixed message a failed flow list produces.
			logf("reading epic progression %q/%q failed: %v", key.RepoPath, key.EpicID, err)
			s.err = errStateUnavailable
			return
		}
		s.progressions[key] = epicProgressionProjection{record: record, found: found}
	}
}

// epicProgressionKey is the canonical lookup key for a Flow's linked epic, and
// false for a Flow that links none. It deliberately does not consult the child
// Bead id: progression is keyed by epic, and a child-only link has no epic to
// read state for.
func epicProgressionKey(flow *Flow) (flowstore.EpicProgressionKey, bool) {
	epicID := strings.TrimSpace(flow.Record.Bead.EpicID)
	if epicID == "" {
		return flowstore.EpicProgressionKey{}, false
	}
	return flowstore.EpicProgressionKey{RepoPath: flow.RepoPath, EpicID: epicID}, true
}

func (s *snapshot) addRepo(repo *Repo) {
	s.repoByPath[repo.Path] = repo
	s.repos = append(s.repos, repo)
}

// bounds reports, for this snapshot, the largest each list field can resolve
// to and the widest each scalar field can serialize to. inspectCost combines
// these to bound a query's result before executing it, so every value here
// must be an upper bound — never a sample or an average.
func (s *snapshot) bounds() resultBounds {
	limits := resultBounds{
		repos:  len(s.repos),
		flows:  len(s.flows),
		values: fieldValueBytes{},
	}
	for _, flows := range s.flowsByRepo {
		limits.flowsPerRepo = maxInt(limits.flowsPerRepo, len(flows))
	}
	for _, repo := range s.repos {
		limits.values.observeRepo(repo)
	}
	for _, flow := range s.flows {
		limits.values.observeFlow(flow)
		// Measured on OrderedPhases, not Record.Phases, because that is what
		// Flow.phases resolves. A record whose rows share a phase id — the
		// pre-normalization shape flowstore.collapseDuplicatePhaseRows exists
		// to repair — expands there, so len(Record.Phases) is not a bound.
		phases := flowstore.OrderedPhases(flow.Record.Phases)
		limits.phasesPerFlow = maxInt(limits.phasesPerFlow, len(phases))
		for _, phase := range phases {
			limits.dependsOnPerPhase = maxInt(limits.dependsOnPerPhase, len(phase.DependsOn))
			limits.values.observePhase(phase)
		}
	}
	// Measured over the projections rather than the Flows: many Flows resolve
	// Flow.epicProgression from one row, and only a found row serializes.
	for _, projection := range s.progressions {
		if !projection.found {
			continue
		}
		limits.values.observeEpicProgression(projection.record)
	}
	return limits
}

// Serialized widths for the non-string scalars the schema exposes. These are
// fixed by their encoding; only strings vary with the data.
const (
	boolValueBytes     = 5  // "false"
	intValueBytes      = 20 // int64 with a sign
	dateTimeValueBytes = 40 // RFC3339Nano, quoted
)

// fieldValueBytes is the widest one value of each scalar field serializes to
// in this snapshot, keyed "Type.field".
//
// Counting resolved values alone does not bound a response: Flow.instructions
// and Phase.notes are unbounded agent-supplied text, so a query that resolves
// only a few thousand values can still serialize to hundreds of megabytes once
// re-entry repeats each one.
type fieldValueBytes map[string]int64

// observe records one value's serialized width, inserting a zero-width entry
// the first time so "no key" means "field this snapshot has never seen" —
// which is drift, and gets the pessimistic fallback — rather than "field that
// happens to be empty everywhere", which really is zero bytes wide.
func (f fieldValueBytes) observe(field string, size int) {
	if current, seen := f[field]; !seen || int64(size) > current {
		f[field] = int64(size)
	}
}

// observeText records a string field at its *encoded* width. Measuring len()
// would undercount by up to 6x: encoding/json escapes <, >, & and every
// control byte to a six-byte \uXXXX, and a Flow's markdown instructions are
// full of the first three.
func (f fieldValueBytes) observeText(field, value string) {
	f.observe(field, jsonStringBytes(value))
}

func (f fieldValueBytes) observeRepo(repo *Repo) {
	f.observeText("Repo.id", repo.Path)
	f.observeText("Repo.path", repo.Path)
	f.observeText("Repo.displayName", repo.DisplayName)
	f.observe("Repo.isBare", boolValueBytes)
	f.observe("Repo.inScanRoot", boolValueBytes)
}

func (f fieldValueBytes) observeFlow(flow *Flow) {
	record := flow.Record
	f.observeText("Flow.id", record.FlowID)
	f.observeText("Flow.title", record.Title)
	f.observeText("Flow.instructions", record.Instructions)
	f.observeText("Flow.status", record.Status)
	f.observeText("Flow.repoPath", flow.RepoPath)
	f.observeText("Flow.worktreePath", record.WorktreePath)
	f.observeText("Flow.branch", record.Branch)
	f.observeText("Flow.baseRef", record.BaseRef)
	f.observeText("Flow.commit", record.Commit)
	f.observeText("Flow.presetName", record.PresetName)
	f.observeText("Flow.planId", record.PlanID)
	f.observe("Flow.autoMode", boolValueBytes)
	f.observe("Flow.createdAt", dateTimeValueBytes)
	f.observe("Flow.updatedAt", dateTimeValueBytes)

	f.observeText("Issue.provider", record.Issue.Provider)
	f.observe("Issue.number", intValueBytes)
	f.observeText("Issue.url", record.Issue.URL)

	f.observeText("PullRequest.provider", record.PR.Provider)
	f.observe("PullRequest.number", intValueBytes)
	f.observeText("PullRequest.url", record.PR.URL)
	f.observeText("PullRequest.headBranch", record.PR.HeadBranch)
	f.observeText("PullRequest.baseBranch", record.PR.BaseBranch)
	f.observeText("PullRequest.status", record.PR.Status)

	f.observeText("Merge.status", record.Merge.Status)
	f.observeText("Merge.commit", record.Merge.Commit)
	f.observe("Merge.mergedAt", dateTimeValueBytes)

	// The persisted link text, which is what BeadLink resolves verbatim.
	f.observeText("BeadLink.id", record.Bead.ID)
	f.observeText("BeadLink.epicId", record.Bead.EpicID)
}

func (f fieldValueBytes) observeEpicProgression(record flowstore.EpicProgression) {
	f.observe("EpicProgression.enabled", boolValueBytes)
	f.observe("EpicProgression.done", boolValueBytes)
	// Seeded outside the halt branch: a snapshot where nothing is halted still
	// has to register these fields, or they would look like schema drift and
	// take the pessimistic fallback. The message is unbounded agent-supplied
	// text, so it is measured at its encoded width like any other free text.
	f.observeText("EpicProgressionHalt.childBeadId", "")
	f.observeText("EpicProgressionHalt.status", "")
	f.observeText("EpicProgressionHalt.message", "")
	if record.Halt == nil {
		return
	}
	f.observeText("EpicProgressionHalt.childBeadId", record.Halt.ChildBeadID)
	f.observeText("EpicProgressionHalt.status", record.Halt.Status)
	f.observeText("EpicProgressionHalt.message", record.Halt.Message)
}

func (f fieldValueBytes) observePhase(phase flowstore.FlowPhase) {
	f.observeText("Phase.id", phase.PhaseID)
	f.observeText("Phase.parentPhaseId", phase.ParentPhaseID)
	f.observeText("Phase.title", phase.Title)
	f.observeText("Phase.kind", phase.Kind)
	f.observeText("Phase.status", phase.Status)
	f.observe("Phase.order", intValueBytes)
	f.observeText("Phase.outcome", phase.Outcome)
	f.observeText("Phase.notes", phase.Notes)
	f.observeText("Phase.summary", phase.Summary)
	f.observe("Phase.createdAt", dateTimeValueBytes)
	f.observe("Phase.updatedAt", dateTimeValueBytes)
	// Seeded outside the loop: a phase graph with no edges anywhere still has
	// to register the field, or it would look like schema drift.
	f.observe("Phase.dependsOn", 0)
	for _, dependency := range phase.DependsOn {
		f.observeText("Phase.dependsOn", dependency)
	}
}

// jsonStringBytes is the width of value once encoding/json has escaped and
// quoted it.
//
// It counts what encoding/json's appendString would emit with HTML escaping on
// — the default for json.Encoder — rather than calling json.Marshal and
// measuring the result. Marshalling to measure would allocate an escaped copy
// of the value: Flow.instructions and Phase.notes are unbounded agent-supplied
// markdown, bounds() walks every one of them on every request whether or not
// the query selects them, and a string of `<` escapes to six times its size.
// That is a per-request multiple of the store's whole text, taken *before* any
// budget has been consulted, across up to MaxInFlight requests at once.
//
// The rules are duplicated from the encoder rather than derived from it, so
// TestJSONStringBytesMatchesTheEncoder pins the two together over every byte
// and every escape class: a width that drifts *below* the encoder's is exactly
// the undercount the byte budget exists to prevent.
func jsonStringBytes(value string) int {
	size := 2 // the surrounding quotes
	for i := 0; i < len(value); {
		if b := value[i]; b < utf8.RuneSelf {
			switch {
			case jsonPlainByte(b):
				size++
			case b == '"' || b == '\\' || b == '\b' || b == '\f' ||
				b == '\n' || b == '\r' || b == '\t':
				size += 2
			default:
				// Every other byte below 0x20 becomes \u00XX.
				size += 6
			}
			i++
			continue
		}
		decoded, width := utf8.DecodeRuneInString(value[i:])
		switch {
		case decoded == utf8.RuneError && width == 1:
			size += 6 // \ufffd, one per invalid byte
		case decoded == '\u2028' || decoded == '\u2029':
			// Escaped unconditionally: valid in JSON, unsafe in JSONP.
			size += 6
		default:
			size += width
		}
		i += width
	}
	return size
}

// jsonPlainByte reports whether the encoder passes an ASCII byte through
// untouched. It is encoding/json's htmlSafeSet: printable ASCII except the two
// JSON metacharacters and the three HTML ones.
func jsonPlainByte(b byte) bool {
	switch b {
	case '"', '\\', '<', '>', '&':
		return false
	}
	return b >= 0x20
}

// Repos returns the union of scanned and flow-derived repositories.
func (s *snapshot) Repos() []*Repo { return s.repos }

// Repo resolves a client-supplied id, normalizing it first so an unnormalized
// argument still matches.
func (s *snapshot) Repo(id string) (*Repo, bool) {
	path, ok := normalizePath(id)
	if !ok {
		return nil, false
	}
	repo, ok := s.repoByPath[path]
	return repo, ok
}

// Flows returns every flow in store order (UpdatedAt descending).
func (s *snapshot) Flows() []*Flow { return s.flows }

// Flow resolves one flow by its flow id.
func (s *snapshot) Flow(id string) (*Flow, bool) {
	flow, ok := s.flowByID[strings.TrimSpace(id)]
	return flow, ok
}

// EpicProgression returns the persisted progression row for the Flow's linked
// epic. It reports false both for a Flow that links no epic and for a link
// whose row is absent: neither case fabricates an epic or a progression record,
// and "no row" is not the same claim as "disabled".
func (s *snapshot) EpicProgression(flow *Flow) (flowstore.EpicProgression, bool) {
	key, ok := epicProgressionKey(flow)
	if !ok {
		return flowstore.EpicProgression{}, false
	}
	projection, loaded := s.progressions[key]
	if !loaded || !projection.found {
		return flowstore.EpicProgression{}, false
	}
	return projection.record, true
}

// FlowsForRepo serves both flows(repoId:) and Repo.flows from the same index,
// so a single response is internally consistent.
func (s *snapshot) FlowsForRepo(repoID string) []*Flow {
	path, ok := normalizePath(repoID)
	if !ok {
		return nil
	}
	return s.flowsByRepo[path]
}

// normalizePath is the single path rule applied to scanner output, to
// FlowRecord.RepoPath, and to client-supplied repo ids. Without one rule the
// same repo can appear twice — once scanned, once synthesized — and flow.repo
// links to the wrong one.
func normalizePath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

// sortRepos orders the whole union deterministically. The union cannot inherit
// scanner.Scan's order: synthesized entries were never sorted, DisplayName is
// not unique across the union (a depth-2 scanned repo is "parent/child" while
// a synthesized entry for the same repo is "child"), and scanner sorts with
// the unstable sort.Slice.
func sortRepos(repos []*Repo) {
	sort.SliceStable(repos, func(i, j int) bool {
		left := strings.ToLower(repos[i].DisplayName)
		right := strings.ToLower(repos[j].DisplayName)
		if left == right {
			return repos[i].Path < repos[j].Path
		}
		return left < right
	})
}
