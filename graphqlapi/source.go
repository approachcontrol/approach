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
		if _, exists := snap.flowByID[record.FlowID]; !exists {
			snap.flowByID[record.FlowID] = flow
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
