package graphqlapi

import (
	"context"

	"github.com/graphql-go/graphql"

	"github.com/approachcontrol/approach/flowstore"
)

type contextKey struct{ name string }

var snapshotContextKey = &contextKey{name: "graphqlapi.snapshot"}

func withSnapshot(ctx context.Context, snap *snapshot) context.Context {
	return context.WithValue(ctx, snapshotContextKey, snap)
}

// snapshotFrom returns the request's read model. A store failure recorded
// while building it surfaces here as the sanitized message: graphql-go copies
// resolver error strings verbatim into the errors array, so sanitization has
// to happen inside the resolver rather than after execution.
func snapshotFrom(ctx context.Context) (*snapshot, error) {
	snap, _ := ctx.Value(snapshotContextKey).(*snapshot)
	if snap == nil {
		return nil, errStateUnavailable
	}
	if snap.err != nil {
		return nil, snap.err
	}
	return snap, nil
}

// newSchema builds the read-only schema. There is deliberately no Mutation or
// Subscription root type: read-only is structural, not a runtime check.
func newSchema() (graphql.Schema, error) {
	issueType := graphql.NewObject(graphql.ObjectConfig{
		Name:        "Issue",
		Description: "Agent-reported issue metadata for a Flow.",
		Fields: graphql.Fields{
			"provider": stringField("Issue provider, e.g. github.", func(issue flowstore.Issue) string { return issue.Provider }),
			"number": &graphql.Field{
				Type: graphql.Int,
				Resolve: func(p graphql.ResolveParams) (any, error) {
					issue, ok := p.Source.(flowstore.Issue)
					if !ok || issue.Number == 0 {
						return nil, nil
					}
					return issue.Number, nil
				},
			},
			"url": stringField("Issue URL.", func(issue flowstore.Issue) string { return issue.URL }),
		},
	})

	pullRequestType := graphql.NewObject(graphql.ObjectConfig{
		Name:        "PullRequest",
		Description: "Agent-reported pull request metadata for a Flow.",
		Fields: graphql.Fields{
			"provider": stringField("Pull request provider, e.g. github.", func(pr flowstore.PullRequest) string { return pr.Provider }),
			"number": &graphql.Field{
				Type: graphql.Int,
				Resolve: func(p graphql.ResolveParams) (any, error) {
					pr, ok := p.Source.(flowstore.PullRequest)
					if !ok || pr.Number == 0 {
						return nil, nil
					}
					return pr.Number, nil
				},
			},
			"url":        stringField("Pull request URL.", func(pr flowstore.PullRequest) string { return pr.URL }),
			"headBranch": stringField("Pull request head branch.", func(pr flowstore.PullRequest) string { return pr.HeadBranch }),
			"baseBranch": stringField("Pull request base branch.", func(pr flowstore.PullRequest) string { return pr.BaseBranch }),
			"status":     stringField("Agent-supplied pull request status. Free text; not a closed set.", func(pr flowstore.PullRequest) string { return pr.Status }),
		},
	})

	mergeType := graphql.NewObject(graphql.ObjectConfig{
		Name:        "Merge",
		Description: "Agent-reported merge metadata for a Flow.",
		Fields: graphql.Fields{
			"status": stringField("Merge status: pending, merged, or blocked.", func(merge flowstore.Merge) string { return merge.Status }),
			"commit": stringField("Merge commit SHA.", func(merge flowstore.Merge) string { return merge.Commit }),
			"mergedAt": &graphql.Field{
				Type:        graphql.DateTime,
				Description: "When the merge landed; null while unmerged.",
				Resolve: func(p graphql.ResolveParams) (any, error) {
					merge, ok := p.Source.(flowstore.Merge)
					if !ok || merge.MergedAt == nil {
						return nil, nil
					}
					return *merge.MergedAt, nil
				},
			},
		},
	})

	beadLinkType := graphql.NewObject(graphql.ObjectConfig{
		Name: "BeadLink",
		Description: "The Beads issue a Flow tracks. Both ids are the persisted link text, " +
			"verbatim; the API never reads Beads itself.",
		Fields: graphql.Fields{
			"id": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.ID),
				Description: "Child Bead id. A linked Flow always has one — the link is what makes bead non-null.",
				Resolve:     valueResolver(func(link flowstore.BeadLink) (any, error) { return link.ID, nil }),
			},
			"epicId": &graphql.Field{
				Type:        graphql.ID,
				Description: "Parent epic Bead id; null when the link records only a child Bead.",
				Resolve:     valueResolver(func(link flowstore.BeadLink) (any, error) { return nullableString(link.EpicID), nil }),
			},
		},
	})

	epicProgressionHaltType := graphql.NewObject(graphql.ObjectConfig{
		Name:        "EpicProgressionHalt",
		Description: "The durable reason auto-progression stopped for an epic.",
		Fields: graphql.Fields{
			"childBeadId": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.ID),
				Description: "The child Bead whose Flow stopped progression.",
				Resolve:     valueResolver(func(halt flowstore.EpicProgressionHalt) (any, error) { return halt.ChildBeadID, nil }),
			},
			"status": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.String),
				Description: "The child Flow status that halted progression: blocked, needs_attention, closed, or abandoned.",
				Resolve:     valueResolver(func(halt flowstore.EpicProgressionHalt) (any, error) { return halt.Status, nil }),
			},
			"message": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.String),
				Description: "Human-readable halt reason. Free text; the store only requires it to be non-empty.",
				Resolve:     valueResolver(func(halt flowstore.EpicProgressionHalt) (any, error) { return halt.Message, nil }),
			},
		},
	})

	epicProgressionType := graphql.NewObject(graphql.ObjectConfig{
		Name: "EpicProgression",
		Description: "Durable auto-progression state for the epic a Flow's Bead link names. " +
			"Read-only: nothing in this schema enables, disables, or halts it.",
		Fields: graphql.Fields{
			"enabled": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.Boolean),
				Description: "Whether progression is currently active for this epic.",
				Resolve: valueResolver(func(record flowstore.EpicProgression) (any, error) {
					return record.Enabled, nil
				}),
			},
			"done": &graphql.Field{
				Type: graphql.NewNonNull(graphql.Boolean),
				Description: "The persisted completion flag. It is never inferred from enabled: a manually " +
					"disabled epic is not done.",
				Resolve: valueResolver(func(record flowstore.EpicProgression) (any, error) {
					return record.Done, nil
				}),
			},
			"halt": &graphql.Field{
				Type:        epicProgressionHaltType,
				Description: "Why progression stopped; null when it stopped for no recorded reason or never stopped.",
				Resolve: valueResolver(func(record flowstore.EpicProgression) (any, error) {
					if record.Halt == nil {
						return nil, nil
					}
					return *record.Halt, nil
				}),
			},
		},
	})

	phaseType := graphql.NewObject(graphql.ObjectConfig{
		Name:        "Phase",
		Description: "One phase in a Flow's phase graph.",
		Fields: graphql.Fields{
			"id": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.ID),
				Description: "Normalized phase id, unique within the Flow.",
				Resolve:     phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return phase.PhaseID, nil }),
			},
			"parentPhaseId": &graphql.Field{
				Type:        graphql.String,
				Description: "Parent phase id for child phases; null for top-level phases.",
				Resolve:     phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return nullableString(phase.ParentPhaseID), nil }),
			},
			"title": &graphql.Field{
				Type:    graphql.NewNonNull(graphql.String),
				Resolve: phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return phase.Title, nil }),
			},
			"kind": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.String),
				Description: "Phase kind, e.g. plan, plan_review, implementation, merge. See docs/flow-phases.md.",
				Resolve:     phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return phase.Kind, nil }),
			},
			"status": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.String),
				Description: "Phase status: pending, ready, running, needs_attention, completed, blocked, or skipped.",
				Resolve:     phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return phase.Status, nil }),
			},
			"order": &graphql.Field{
				Type:    graphql.NewNonNull(graphql.Int),
				Resolve: phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return phase.Order, nil }),
			},
			"dependsOn": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(graphql.String))),
				Description: "Phase ids this phase waits on. Empty for phases with no edges; never null.",
				Resolve: phaseResolver(func(phase flowstore.FlowPhase) (any, error) {
					if phase.DependsOn == nil {
						return []string{}, nil
					}
					return phase.DependsOn, nil
				}),
			},
			"outcome": &graphql.Field{
				Type:        graphql.String,
				Description: "Phase outcome. Constrained for plan_review phases; free text otherwise.",
				Resolve:     phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return nullableString(phase.Outcome), nil }),
			},
			"notes": &graphql.Field{
				Type:    graphql.String,
				Resolve: phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return nullableString(phase.Notes), nil }),
			},
			"summary": &graphql.Field{
				Type:    graphql.String,
				Resolve: phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return nullableString(phase.Summary), nil }),
			},
			"createdAt": &graphql.Field{
				Type:    graphql.NewNonNull(graphql.DateTime),
				Resolve: phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return phase.CreatedAt, nil }),
			},
			"updatedAt": &graphql.Field{
				Type:    graphql.NewNonNull(graphql.DateTime),
				Resolve: phaseResolver(func(phase flowstore.FlowPhase) (any, error) { return phase.UpdatedAt, nil }),
			},
		},
	})

	var repoType *graphql.Object
	var flowType *graphql.Object

	repoType = graphql.NewObject(graphql.ObjectConfig{
		Name:        "Repo",
		Description: "A repository discovered under the scan root, or referenced by a Flow.",
		Fields: graphql.FieldsThunk(func() graphql.Fields {
			return graphql.Fields{
				"id": &graphql.Field{
					Type:        graphql.NewNonNull(graphql.ID),
					Description: "The repository's normalized absolute path.",
					Resolve:     repoResolver(func(repo *Repo) (any, error) { return repo.Path, nil }),
				},
				"path": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.String),
					Resolve: repoResolver(func(repo *Repo) (any, error) { return repo.Path, nil }),
				},
				"displayName": &graphql.Field{
					Type:        graphql.NewNonNull(graphql.String),
					Description: "Scanner display name (parent/child at depth 2), or the base name for repos outside the scan root.",
					Resolve:     repoResolver(func(repo *Repo) (any, error) { return repo.DisplayName, nil }),
				},
				"isBare": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.Boolean),
					Resolve: repoResolver(func(repo *Repo) (any, error) { return repo.IsBare, nil }),
				},
				"inScanRoot": &graphql.Field{
					Type:        graphql.NewNonNull(graphql.Boolean),
					Description: "False for repositories known only because a Flow record references them.",
					Resolve:     repoResolver(func(repo *Repo) (any, error) { return repo.InScanRoot, nil }),
				},
				"flows": &graphql.Field{
					Type:        graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(flowType))),
					Description: "Flows for this repository, most recently updated first.",
					Resolve: func(p graphql.ResolveParams) (any, error) {
						repo, ok := p.Source.(*Repo)
						if !ok {
							return nil, errStateUnavailable
						}
						snap, err := snapshotFrom(p.Context)
						if err != nil {
							return nil, err
						}
						return flowList(snap.FlowsForRepo(repo.Path)), nil
					},
				},
			}
		}),
	})

	flowType = graphql.NewObject(graphql.ObjectConfig{
		Name:        "Flow",
		Description: "A persisted Flow record and its phase graph.",
		Fields: graphql.FieldsThunk(func() graphql.Fields {
			return graphql.Fields{
				"id": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.ID),
					Resolve: flowResolver(func(flow *Flow) (any, error) { return flow.Record.FlowID, nil }),
				},
				"title": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.String),
					Resolve: flowResolver(func(flow *Flow) (any, error) { return flow.Record.Title, nil }),
				},
				"instructions": &graphql.Field{
					Type:    graphql.String,
					Resolve: flowResolver(func(flow *Flow) (any, error) { return nullableString(flow.Record.Instructions), nil }),
				},
				"status": &graphql.Field{
					Type:        graphql.NewNonNull(graphql.String),
					Description: "Derived Flow status: pending, in_progress, needs_attention, blocked, completed, merged, or abandoned.",
					Resolve:     flowResolver(func(flow *Flow) (any, error) { return flow.Record.Status, nil }),
				},
				"repoPath": &graphql.Field{
					Type:        graphql.NewNonNull(graphql.String),
					Description: "Normalized absolute repo path; always resolvable via repo(id:).",
					Resolve:     flowResolver(func(flow *Flow) (any, error) { return flow.RepoPath, nil }),
				},
				"repo": &graphql.Field{
					Type:        graphql.NewNonNull(repoType),
					Description: "The repository this Flow belongs to, synthesized when it lies outside the scan root.",
					Resolve: func(p graphql.ResolveParams) (any, error) {
						flow, ok := p.Source.(*Flow)
						if !ok {
							return nil, errStateUnavailable
						}
						snap, err := snapshotFrom(p.Context)
						if err != nil {
							return nil, err
						}
						repo, ok := snap.Repo(flow.RepoPath)
						if !ok {
							return nil, errStateUnavailable
						}
						return repo, nil
					},
				},
				"worktreePath": &graphql.Field{
					Type:    graphql.String,
					Resolve: flowResolver(func(flow *Flow) (any, error) { return nullableString(flow.Record.WorktreePath), nil }),
				},
				"branch": &graphql.Field{
					Type:    graphql.String,
					Resolve: flowResolver(func(flow *Flow) (any, error) { return nullableString(flow.Record.Branch), nil }),
				},
				"baseRef": &graphql.Field{
					Type:    graphql.String,
					Resolve: flowResolver(func(flow *Flow) (any, error) { return nullableString(flow.Record.BaseRef), nil }),
				},
				"commit": &graphql.Field{
					Type:    graphql.String,
					Resolve: flowResolver(func(flow *Flow) (any, error) { return nullableString(flow.Record.Commit), nil }),
				},
				"presetName": &graphql.Field{
					Type:    graphql.String,
					Resolve: flowResolver(func(flow *Flow) (any, error) { return nullableString(flow.Record.PresetName), nil }),
				},
				"planId": &graphql.Field{
					Type:        graphql.String,
					Description: "Linked saved-plan id. The plan body itself is not exposed.",
					Resolve:     flowResolver(func(flow *Flow) (any, error) { return nullableString(flow.Record.PlanID), nil }),
				},
				"autoMode": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.Boolean),
					Resolve: flowResolver(func(flow *Flow) (any, error) { return flow.Record.AutoMode, nil }),
				},
				"bead": &graphql.Field{
					Type:        beadLinkType,
					Description: "The Flow's persisted Beads linkage; null when no Bead is linked.",
					Resolve: flowResolver(func(flow *Flow) (any, error) {
						if flow.Record.Bead.ID == "" {
							return nil, nil
						}
						return flow.Record.Bead, nil
					}),
				},
				"epicProgression": &graphql.Field{
					Type: epicProgressionType,
					Description: "Auto-progression state for the epic this Flow's Bead link names. Null when the " +
						"Flow links no epic and when no progression row exists for it — an absent row is not a " +
						"disabled epic, and neither is synthesized into one.",
					Resolve: func(p graphql.ResolveParams) (any, error) {
						flow, ok := p.Source.(*Flow)
						if !ok {
							return nil, errStateUnavailable
						}
						snap, err := snapshotFrom(p.Context)
						if err != nil {
							return nil, err
						}
						record, ok, err := snap.EpicProgression(flow)
						if err != nil {
							// Scoped to this field on this Flow: an unreadable
							// progression row nulls it and reports an error
							// entry, and leaves the rest of the response intact.
							return nil, err
						}
						if !ok {
							return nil, nil
						}
						return record, nil
					},
				},
				"issue": &graphql.Field{
					Type: issueType,
					Resolve: flowResolver(func(flow *Flow) (any, error) {
						issue := flow.Record.Issue
						if issue.Provider == "" && issue.Number == 0 && issue.URL == "" {
							return nil, nil
						}
						return issue, nil
					}),
				},
				"pullRequest": &graphql.Field{
					Type: pullRequestType,
					Resolve: flowResolver(func(flow *Flow) (any, error) {
						pr := flow.Record.PR
						if pr.Provider == "" && pr.Number == 0 && pr.URL == "" &&
							pr.HeadBranch == "" && pr.BaseBranch == "" && pr.Status == "" {
							return nil, nil
						}
						return pr, nil
					}),
				},
				"merge": &graphql.Field{
					Type: mergeType,
					Resolve: flowResolver(func(flow *Flow) (any, error) {
						merge := flow.Record.Merge
						// The store forces Status to "pending" when empty, so
						// an all-empty check would make merge never null.
						if merge.Status == flowstore.MergePending && merge.Commit == "" && merge.MergedAt == nil {
							return nil, nil
						}
						return merge, nil
					}),
				},
				"phases": &graphql.Field{
					Type:        graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(phaseType))),
					Description: "Phases in graph order, with child phases grouped directly below their parent.",
					Resolve: flowResolver(func(flow *Flow) (any, error) {
						phases := flowstore.OrderedPhases(flow.Record.Phases)
						if phases == nil {
							return []flowstore.FlowPhase{}, nil
						}
						return phases, nil
					}),
				},
				"currentPhase": &graphql.Field{
					Type: phaseType,
					Description: "The phase the Flow is sitting on: the first phase in graph order whose status is " +
						"ready, running, needs_attention, or blocked. Not necessarily executing — blocked and " +
						"needs_attention count. Null when no phase matches.",
					Resolve: flowResolver(func(flow *Flow) (any, error) {
						phase, ok := flowstore.NextActionablePhase(flow.Record)
						if !ok {
							return nil, nil
						}
						return phase, nil
					}),
				},
				"createdAt": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.DateTime),
					Resolve: flowResolver(func(flow *Flow) (any, error) { return flow.Record.CreatedAt, nil }),
				},
				"updatedAt": &graphql.Field{
					Type:    graphql.NewNonNull(graphql.DateTime),
					Resolve: flowResolver(func(flow *Flow) (any, error) { return flow.Record.UpdatedAt, nil }),
				},
			}
		}),
	})

	queryType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"repos": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(repoType))),
				Description: "Every known repository, sorted by display name then path.",
				Resolve: func(p graphql.ResolveParams) (any, error) {
					snap, err := snapshotFrom(p.Context)
					if err != nil {
						return nil, err
					}
					repos := snap.Repos()
					if repos == nil {
						return []*Repo{}, nil
					}
					return repos, nil
				},
			},
			"repo": &graphql.Field{
				Type:        repoType,
				Description: "One repository by normalized absolute path. Null when this snapshot does not know it.",
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					snap, err := snapshotFrom(p.Context)
					if err != nil {
						return nil, err
					}
					repo, ok := snap.Repo(stringArg(p, "id"))
					if !ok {
						return nil, nil
					}
					return repo, nil
				},
			},
			"flows": &graphql.Field{
				Type:        graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(flowType))),
				Description: "Flows, most recently updated first, optionally narrowed to one repository.",
				Args: graphql.FieldConfigArgument{
					"repoId": &graphql.ArgumentConfig{Type: graphql.ID},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					snap, err := snapshotFrom(p.Context)
					if err != nil {
						return nil, err
					}
					if repoID, ok := p.Args["repoId"]; ok && repoID != nil {
						return flowList(snap.FlowsForRepo(stringArg(p, "repoId"))), nil
					}
					return flowList(snap.Flows()), nil
				},
			},
			"flow": &graphql.Field{
				Type:        flowType,
				Description: "One Flow by id. Null when this snapshot does not know it.",
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					snap, err := snapshotFrom(p.Context)
					if err != nil {
						return nil, err
					}
					flow, ok := snap.Flow(stringArg(p, "id"))
					if !ok {
						return nil, nil
					}
					return flow, nil
				},
			},
		},
	})

	return graphql.NewSchema(graphql.SchemaConfig{Query: queryType})
}

func flowList(flows []*Flow) []*Flow {
	if flows == nil {
		return []*Flow{}
	}
	return flows
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func stringArg(p graphql.ResolveParams, name string) string {
	value, _ := p.Args[name].(string)
	return value
}

// stringField builds a nullable string field over a value-typed source struct.
func stringField[T any](description string, get func(T) string) *graphql.Field {
	return &graphql.Field{
		Type:        graphql.String,
		Description: description,
		Resolve: func(p graphql.ResolveParams) (any, error) {
			source, ok := p.Source.(T)
			if !ok {
				return nil, nil
			}
			return nullableString(get(source)), nil
		},
	}
}

// valueResolver adapts an object type whose source is a value-typed flowstore
// struct rather than a snapshot pointer. It reports errStateUnavailable on a
// source-type mismatch, the way repoResolver and flowResolver do — not the
// nil, nil that stringField returns.
func valueResolver[T any](get func(T) (any, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		source, ok := p.Source.(T)
		if !ok {
			return nil, errStateUnavailable
		}
		return get(source)
	}
}

func repoResolver(get func(*Repo) (any, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		repo, ok := p.Source.(*Repo)
		if !ok {
			return nil, errStateUnavailable
		}
		return get(repo)
	}
}

func flowResolver(get func(*Flow) (any, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		flow, ok := p.Source.(*Flow)
		if !ok {
			return nil, errStateUnavailable
		}
		return get(flow)
	}
}

func phaseResolver(get func(flowstore.FlowPhase) (any, error)) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		phase, ok := p.Source.(flowstore.FlowPhase)
		if !ok {
			return nil, errStateUnavailable
		}
		return get(phase)
	}
}
