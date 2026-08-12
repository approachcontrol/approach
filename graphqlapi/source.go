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
// (UpdatedAt descending). A failure is a real failure of the primary data
// source and surfaces to clients as a sanitized GraphQL error.
type FlowSource func() ([]flowstore.FlowRecord, error)

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

// snapshot is the immutable read model for exactly one request: one scan plus
// one store list, indexed for pure map and slice reads by every resolver.
type snapshot struct {
	repos       []*Repo
	repoByPath  map[string]*Repo
	flows       []*Flow
	flowByID    map[string]*Flow
	flowsByRepo map[string][]*Flow
	err         error
}

// buildSnapshot reads both sources once and indexes the result. logf receives
// the unsanitized failure detail; it is never nil-checked by callers below, so
// buildSnapshot substitutes a no-op.
func buildSnapshot(repos RepoSource, flows FlowSource, logf func(string, ...any)) *snapshot {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	snap := &snapshot{
		repoByPath:  make(map[string]*Repo),
		flowByID:    make(map[string]*Flow),
		flowsByRepo: make(map[string][]*Flow),
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
		logf("reading flow records failed: %v", err)
		snap.err = errStateUnavailable
		return snap
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
	return snap
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
