#!/usr/bin/env bash
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
MAX_COUNT=1000
count=1
root=""
branches=8
worktrees=2
stashes=3

# ── Argument parsing ─────────────────────────────────────────────────────────
usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Generate test git repositories for approach manual testing.

Options:
  --count N       Number of repos to generate (1–$MAX_COUNT, default: $count)
  --root DIR      Output directory (default: temp dir)
  --branches N    Branches per repo (min 8, default: $branches)
  --worktrees N   Additional worktrees per repo (min 2, default: $worktrees)
  --stashes N     Stashes per repo (default: $stashes)
  -h, --help      Show this help
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case $1 in
        --count)    count="$2"; shift 2 ;;
        --root)     root="$2"; shift 2 ;;
        --branches) branches="$2"; shift 2 ;;
        --worktrees) worktrees="$2"; shift 2 ;;
        --stashes)  stashes="$2"; shift 2 ;;
        -h|--help)  usage ;;
        *)          echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [[ $count -lt 1 || $count -gt $MAX_COUNT ]]; then
    echo "Error: --count must be between 1 and $MAX_COUNT"
    exit 1
fi
if [[ $branches -lt 8 ]]; then
    echo "Error: --branches must be at least 8 (base set)"
    exit 1
fi
if [[ $worktrees -lt 2 ]]; then
    echo "Error: --worktrees must be at least 2 (base set)"
    exit 1
fi

if [[ -z "$root" ]]; then
    root=$(mktemp -d "${TMPDIR:-/tmp}/approach-test-repos-XXXXXXXXXX")
fi
mkdir -p "$root"

# ── Stash message pool (varied lengths) ──────────────────────────────────────
STASH_MESSAGES=(
    "WIP: auth fix"
    "Quick typo correction"
    "Experiment with new color scheme"
    "Refactoring database connection pooling to support read replicas"
    "Debugging intermittent test failure in CI pipeline"
    "WIP: updating API response format for v2 endpoints"
    "Partial migration of user service from REST to gRPC — handlers done, tests pending"
    "Prototyping server-sent events for real-time notifications in the dashboard component"
    "WIP: migrating the legacy authentication middleware from session-based tokens to JWT with refresh token rotation — need to finish the token revocation endpoint and update integration tests"
    "Refactoring the payment processing pipeline to support idempotency keys, retry logic with exponential backoff, and dead-letter queues for failed transactions across all payment providers"
    "Investigating memory leak in websocket connection handler — added profiling instrumentation to track goroutine lifecycle and connection pool saturation under sustained load testing conditions"
    "WIP: implementing multi-tenant data isolation layer with row-level security policies, tenant-scoped connection pooling, cross-tenant query prevention middleware, and automated tenant provisioning workflow including schema migrations and seed data"
    "Stashing changes to config parser before rebasing"
    "Half-done CSS grid layout for settings page"
    "Temporary workaround for upstream API rate limiting — caching responses in Redis with 5-minute TTL and circuit breaker pattern for graceful degradation when the cache is cold or the upstream service is experiencing elevated error rates above the configured threshold"
)

# ── Branch name pools ────────────────────────────────────────────────────────
PREFIXES=(feature fix chore refactor docs test)
DESCRIPTORS=(
    auth-migration api-v2 caching-layer search-index user-profiles
    dark-mode rate-limiter webhook-handler error-reporting config-parser
    db-connection ci-pipeline deployment-script log-aggregation metrics-dashboard
    session-management file-upload permission-system batch-processor notification-service
    data-export health-check load-balancer schema-migration audit-logging
)

# ── Helpers ───────────────────────────────────────────────────────────────────
log_step() {
    echo "  $1"
}

run_quiet() {
    local dir="$1"; shift
    git -C "$dir" "$@" >/dev/null 2>&1
}

write_file() {
    local filepath="$1" content="$2"
    mkdir -p "$(dirname "$filepath")"
    printf '%s\n' "$content" > "$filepath"
}

pick_stash_message() {
    local idx=$(( RANDOM % ${#STASH_MESSAGES[@]} ))
    echo "${STASH_MESSAGES[$idx]}"
}

pick_branch_name() {
    local n="$1"
    local prefix="${PREFIXES[$(( RANDOM % ${#PREFIXES[@]} ))]}"
    local desc="${DESCRIPTORS[$(( RANDOM % ${#DESCRIPTORS[@]} ))]}"
    echo "${prefix}/${desc}-$(printf '%03d' "$n")"
}

# Returns a state name based on weighted random selection:
#   clean=30%, ahead=25%, local-only=20%, behind=15%, ahead+behind=10%
pick_branch_state() {
    local roll=$(( RANDOM % 100 ))
    if   [[ $roll -lt 30 ]]; then echo "clean"
    elif [[ $roll -lt 55 ]]; then echo "ahead"
    elif [[ $roll -lt 75 ]]; then echo "local-only"
    elif [[ $roll -lt 90 ]]; then echo "behind"
    else                          echo "diverged"
    fi
}

# ── Initial file content ─────────────────────────────────────────────────────
create_initial_files() {
    local repo_dir="$1" name="$2"

    write_file "$repo_dir/README.md" "# $name

A sample project for testing approach.

## Getting Started

Run \`go build ./...\` to compile.
Run \`go test ./...\` to run the test suite."

    write_file "$repo_dir/src/main.go" "package main

import (
	\"fmt\"
	\"os\"
)

func main() {
	fmt.Println(\"$name starting...\")
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, \"error: %v\\n\", err)
		os.Exit(1)
	}
}

func run() error {
	return nil
}"

    write_file "$repo_dir/src/utils.go" "package main

import (
	\"strings\"
	\"time\"
)

func formatTimestamp(t time.Time) string {
	return t.Format(time.RFC3339)
}

func sanitizeInput(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	return s
}

func contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}"

    write_file "$repo_dir/config.yaml" "app:
  name: $name
  port: 8080
  debug: false
database:
  host: localhost
  port: 5432
  name: ${name}_db"

    write_file "$repo_dir/.gitignore" "bin/
*.exe
*.log
.env"
}

# ── Push behind commits via temp clone ────────────────────────────────────────
push_behind_commits() {
    local bare_dir="$1" branch="$2" n="${3:-2}"
    local tmp_clone
    tmp_clone=$(mktemp -d "${TMPDIR:-/tmp}/approach-behind-XXXXXXXXXX")

    git clone --quiet "$bare_dir" "$tmp_clone" 2>/dev/null
    git -C "$tmp_clone" config user.email "colleague@approach.dev"
    git -C "$tmp_clone" config user.name "Colleague"
    run_quiet "$tmp_clone" checkout "$branch"

    for i in $(seq 1 "$n"); do
        write_file "$tmp_clone/src/remote-change-${i}.go" "package main
// Remote change $i for $branch"
        run_quiet "$tmp_clone" add .
        run_quiet "$tmp_clone" commit -m "Remote: update $branch (change $i)"
    done

    run_quiet "$tmp_clone" push origin "$branch"
    rm -rf "$tmp_clone"
}

# ── Generate a single repo ────────────────────────────────────────────────────
generate_repo() {
    local root_dir="$1" name="$2"
    local bare_dir="$root_dir/.remotes/$name.git"
    local repo_dir="$root_dir/$name"

    # ── Phase 1: Bare upstream + clone ──
    log_step "Creating bare upstream..."
    mkdir -p "$bare_dir"
    git init --quiet --bare "$bare_dir"
    git -C "$bare_dir" symbolic-ref HEAD refs/heads/main

    # Clone (creates origin automatically)
    git clone --quiet "$bare_dir" "$repo_dir" 2>/dev/null
    git -C "$repo_dir" config user.email "test@approach.dev"
    git -C "$repo_dir" config user.name "Test User"

    # Initial content
    create_initial_files "$repo_dir" "$name"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Initial project setup"
    run_quiet "$repo_dir" push origin main

    # ── Phase 2: feature/stable (green checkmark) ──
    log_step "Creating feature/stable (clean, even with upstream)..."
    run_quiet "$repo_dir" checkout -b feature/stable
    write_file "$repo_dir/src/stable.go" "package main

func stableFeature() string {
	return \"this feature is complete and pushed\"
}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Add stable feature"
    run_quiet "$repo_dir" push -u origin feature/stable

    # ── Phase 3: feature/ahead (yellow +3/-0) ──
    log_step "Creating feature/ahead (3 unpushed commits)..."
    run_quiet "$repo_dir" checkout main
    run_quiet "$repo_dir" checkout -b feature/ahead
    write_file "$repo_dir/src/ahead.go" "package main

func aheadFeature() {}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Add ahead feature scaffold"
    run_quiet "$repo_dir" push -u origin feature/ahead

    # Add 3 unpushed commits
    write_file "$repo_dir/src/ahead.go" "package main

func aheadFeature() {
	// core logic
	processItems()
}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Implement core logic for ahead feature"

    write_file "$repo_dir/src/ahead.go" "package main

func aheadFeature() {
	// core logic with error handling
	if err := processItems(); err != nil {
		handleError(err)
	}
}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Add error handling"

    write_file "$repo_dir/src/ahead_helper.go" "package main

func processItems() error { return nil }
func handleError(err error) {}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Add helper utilities"

    # ── Phase 4: feature/behind (yellow +0/-2) ──
    log_step "Creating feature/behind (2 commits behind)..."
    run_quiet "$repo_dir" checkout main
    run_quiet "$repo_dir" checkout -b feature/behind
    write_file "$repo_dir/src/behind.go" "package main

func behindFeature() string {
	return \"needs sync\"
}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Add behind feature"
    run_quiet "$repo_dir" push -u origin feature/behind

    push_behind_commits "$bare_dir" "feature/behind" 2
    run_quiet "$repo_dir" fetch origin

    # ── Phase 5: feature/dirty (red dot in worktree) ──
    log_step "Creating feature/dirty (dirty worktree)..."
    run_quiet "$repo_dir" checkout main
    run_quiet "$repo_dir" checkout -b feature/dirty
    write_file "$repo_dir/src/dirty.go" "package main

func dirtyFeature() {}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Add dirty feature placeholder"
    run_quiet "$repo_dir" push -u origin feature/dirty
    run_quiet "$repo_dir" checkout main

    local wt_dirty="$root_dir/${name}-wt-dirty"
    run_quiet "$repo_dir" worktree add "$wt_dirty" feature/dirty

    # Leave dirty changes
    write_file "$wt_dirty/README.md" "# $name (modified in worktree)

This file has been modified with uncommitted changes.
Adding extra lines to create a visible diff."
    write_file "$wt_dirty/src/new-feature.go" "package main

func newUncommittedFeature() {
	// this file is brand new and untracked
}"
    rm -f "$wt_dirty/config.yaml"
    git -C "$wt_dirty" add src/new-feature.go  # stage just the new file (mixed state)

    # ── Phase 6: feature/local-only (purple dot) ──
    log_step "Creating feature/local-only (no upstream)..."
    run_quiet "$repo_dir" branch feature/local-only

    # ── Phase 7: feature/ahead-dirty (yellow + red dots in worktree) ──
    log_step "Creating feature/ahead-dirty (ahead + dirty worktree)..."
    run_quiet "$repo_dir" checkout -b feature/ahead-dirty
    write_file "$repo_dir/src/wip.go" "package main

func wipFeature() {}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Scaffold ahead-dirty feature"
    run_quiet "$repo_dir" push -u origin feature/ahead-dirty
    run_quiet "$repo_dir" checkout main

    local wt_dev="$root_dir/${name}-wt-dev"
    run_quiet "$repo_dir" worktree add "$wt_dev" feature/ahead-dirty

    # Add unpushed commits in the worktree
    write_file "$wt_dev/src/wip.go" "package main

func wipFeature() {
	// experimental implementation
	experimentalLogic()
}"
    run_quiet "$wt_dev" add .
    run_quiet "$wt_dev" commit -m "WIP: experimental feature"

    write_file "$wt_dev/src/wip_helper.go" "package main

func experimentalLogic() {
	// still iterating on this
}"
    run_quiet "$wt_dev" add .
    run_quiet "$wt_dev" commit -m "WIP: iterate on experiment"

    # Leave dirty changes
    printf '\n// uncommitted trailing change\n' >> "$wt_dev/src/wip.go"
    write_file "$wt_dev/src/scratch.go" "package main
// scratch pad - not committed"

    # ── Phase 8: feature/many-commits (7 unpushed, triggers overflow) ──
    log_step "Creating feature/many-commits (7 unpushed commits)..."
    run_quiet "$repo_dir" checkout -b feature/many-commits
    write_file "$repo_dir/src/many.go" "package main

func manyCommitsFeature() {}"
    run_quiet "$repo_dir" add .
    run_quiet "$repo_dir" commit -m "Scaffold many-commits feature"
    run_quiet "$repo_dir" push -u origin feature/many-commits

    for i in $(seq 1 7); do
        printf '\n// iteration %d\n' "$i" >> "$repo_dir/src/many.go"
        run_quiet "$repo_dir" add .
        run_quiet "$repo_dir" commit -m "Iteration $i: incremental improvement to many-commits"
    done

    run_quiet "$repo_dir" checkout main

    # ── Phase 9: Extra branches (weighted random states) ──
    local extra_branches=$(( branches - 8 ))
    if [[ $extra_branches -gt 0 ]]; then
        log_step "Creating $extra_branches extra branches..."
        for n in $(seq 9 "$branches"); do
            local branch_name
            branch_name=$(pick_branch_name "$n")
            local state
            state=$(pick_branch_state)

            run_quiet "$repo_dir" checkout main
            run_quiet "$repo_dir" checkout -b "$branch_name"
            write_file "$repo_dir/src/extra-${n}.go" "package main

// Auto-generated branch $branch_name (state: $state)
func extra${n}() {}"
            run_quiet "$repo_dir" add .
            run_quiet "$repo_dir" commit -m "Add $branch_name"

            case $state in
                clean)
                    run_quiet "$repo_dir" push -u origin "$branch_name"
                    ;;
                ahead)
                    run_quiet "$repo_dir" push -u origin "$branch_name"
                    local ahead_count=$(( RANDOM % 4 + 1 ))
                    for i in $(seq 1 "$ahead_count"); do
                        printf '\n// unpushed change %d\n' "$i" >> "$repo_dir/src/extra-${n}.go"
                        run_quiet "$repo_dir" add .
                        run_quiet "$repo_dir" commit -m "Unpushed: $branch_name iteration $i"
                    done
                    ;;
                local-only)
                    # No push — no upstream
                    ;;
                behind)
                    run_quiet "$repo_dir" push -u origin "$branch_name"
                    local behind_count=$(( RANDOM % 3 + 1 ))
                    push_behind_commits "$bare_dir" "$branch_name" "$behind_count"
                    run_quiet "$repo_dir" fetch origin
                    ;;
                diverged)
                    run_quiet "$repo_dir" push -u origin "$branch_name"
                    # Push remote commits
                    local div_behind=$(( RANDOM % 2 + 1 ))
                    push_behind_commits "$bare_dir" "$branch_name" "$div_behind"
                    run_quiet "$repo_dir" fetch origin
                    # Add local commits
                    local div_ahead=$(( RANDOM % 3 + 1 ))
                    for i in $(seq 1 "$div_ahead"); do
                        printf '\n// local diverged change %d\n' "$i" >> "$repo_dir/src/extra-${n}.go"
                        run_quiet "$repo_dir" add .
                        run_quiet "$repo_dir" commit -m "Local diverge: $branch_name change $i"
                    done
                    ;;
            esac
        done
        run_quiet "$repo_dir" checkout main
    fi

    # ── Phase 10: Extra worktrees ──
    local extra_wt=$(( worktrees - 2 ))
    if [[ $extra_wt -gt 0 ]]; then
        log_step "Creating $extra_wt extra worktrees..."
        for n in $(seq 3 "$worktrees"); do
            local wt_branch="wt/extra-$(printf '%03d' "$n")"
            local wt_path="$root_dir/${name}-wt-extra-$(printf '%03d' "$n")"

            run_quiet "$repo_dir" checkout main
            run_quiet "$repo_dir" checkout -b "$wt_branch"
            write_file "$repo_dir/src/wt-extra-${n}.go" "package main

func wtExtra${n}() {}"
            run_quiet "$repo_dir" add .
            run_quiet "$repo_dir" commit -m "Add worktree branch $wt_branch"
            run_quiet "$repo_dir" push -u origin "$wt_branch"
            run_quiet "$repo_dir" checkout main

            run_quiet "$repo_dir" worktree add "$wt_path" "$wt_branch"

            # Leave dirty changes
            write_file "$wt_path/src/wt-uncommitted-${n}.go" "package main
// uncommitted file in worktree $wt_branch"
            printf '\n// modified in worktree\n' >> "$wt_path/src/wt-extra-${n}.go"
        done
    fi

    # ── Phase 11: Stashes ──
    log_step "Creating $stashes stashes..."
    run_quiet "$repo_dir" checkout main
    for s in $(seq 1 "$stashes"); do
        local msg
        msg=$(pick_stash_message)
        # Modify a file differently each time so the stash has content
        printf '\n// stash change %d: %s\n' "$s" "$msg" >> "$repo_dir/src/utils.go"
        write_file "$repo_dir/src/stash-scratch-${s}.go" "package main
// Scratch file for stash $s"
        run_quiet "$repo_dir" add .
        run_quiet "$repo_dir" stash push -m "$msg"
    done

    # ── Phase 12: Leave main dirty (pending changes) ──
    log_step "Leaving pending changes on main..."
    printf '\n// TODO: pending refactor\nfunc pendingWork() {}\n' >> "$repo_dir/src/utils.go"
    write_file "$repo_dir/TODO.md" "# TODO

- [ ] Finish the refactor
- [ ] Update docs
- [ ] Add integration tests"

    log_step "Done."
}

# ── Main ──────────────────────────────────────────────────────────────────────
echo "Generating $count test repo(s) with $branches branches, $worktrees worktrees, $stashes stashes each..."
echo "Output: $root"
echo ""

for i in $(seq 1 "$count"); do
    name=$(printf "test-repo-%03d" "$i")
    echo "[$i/$count] $name"
    generate_repo "$root" "$name"
    echo ""
done

echo "Done! Generated $count repo(s) in: $root"
echo ""
echo "Run approach with:"
echo "  WORKTREE_ROOT=$root make run"
echo ""
echo "Clean up with:"
echo "  rm -rf $root"
