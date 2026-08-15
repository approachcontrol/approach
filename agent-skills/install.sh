#!/usr/bin/env bash
set -euo pipefail

# Install the bundled Approach agent skills into user-level agent skill
# directories by symlinking (or copying) them out of this repository.
#
# The skills are not auto-discovered by any agent, so they have to be linked
# into the directory each agent scans. Symlinks are the default so the skills
# track the repository checkout instead of going stale on the next pull.

SKILLS=(approach-flow approach-flow-create approach-plan-persist)

# Candidate destinations, used when no --target is given. Only directories
# whose agent home already exists are installed into. Codex reads the shared
# ~/.agents/skills directory, so it needs no entry of its own.
DEFAULT_TARGETS=(
    "$HOME/.claude/skills"
    "$HOME/.agents/skills"
)

# ── Defaults ──────────────────────────────────────────────────────────────────
targets=()
mode="symlink"
dry_run=false
force=false

# ── Argument parsing ─────────────────────────────────────────────────────────
usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Install the Approach agent skills (${SKILLS[*]}) into user-level agent skill
directories.

With no --target, installs into each of these whose parent directory exists:
$(printf '  %s\n' "${DEFAULT_TARGETS[@]}")

Options:
  --target DIR    Destination skill directory (repeatable; overrides defaults)
  --copy          Copy the skills instead of symlinking them
  --dry-run       Print what would change without touching the filesystem
  --force         Replace destinations that are real files or directories
  -h, --help      Show this help
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case $1 in
        --target)   targets+=("$2"); shift 2 ;;
        --copy)     mode="copy"; shift ;;
        --dry-run)  dry_run=true; shift ;;
        --force)    force=true; shift ;;
        -h|--help)  usage ;;
        *)          echo "Unknown option: $1" >&2; exit 2 ;;
    esac
done

# ── Source resolution ────────────────────────────────────────────────────────
source_dir=$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)

for skill in "${SKILLS[@]}"; do
    if [[ ! -f "$source_dir/$skill/SKILL.md" ]]; then
        echo "error: missing $source_dir/$skill/SKILL.md" >&2
        exit 1
    fi
done

if [[ ${#targets[@]} -eq 0 ]]; then
    for candidate in "${DEFAULT_TARGETS[@]}"; do
        [[ -d "$(dirname "$candidate")" ]] && targets+=("$candidate")
    done
fi

if [[ ${#targets[@]} -eq 0 ]]; then
    echo "error: no agent skill directories found; pass --target DIR" >&2
    exit 1
fi

# ── Install ──────────────────────────────────────────────────────────────────
# Physical path of $1, or empty if it does not resolve to a directory.
resolve() {
    (cd -P "$1" >/dev/null 2>&1 && pwd -P) || true
}

run() {
    if [[ $dry_run == true ]]; then
        return 0
    fi
    "$@"
}

installed=0
skipped=0

for target in "${targets[@]}"; do
    echo "→ $target"
    run mkdir -p "$target"

    for skill in "${SKILLS[@]}"; do
        src="$source_dir/$skill"
        dest="$target/$skill"

        if [[ $mode == symlink && -L "$dest" && "$(resolve "$dest")" == "$src" ]]; then
            echo "  ok       $skill (already linked)"
            continue
        fi

        if [[ -e "$dest" || -L "$dest" ]]; then
            # Symlinks are ours to replace — including dangling ones left behind
            # by a moved checkout. Real files and directories need --force.
            if [[ -L "$dest" ]]; then
                echo "  relink   $skill (was $(readlink "$dest"))"
                run rm -f "$dest"
            elif [[ $force == true ]]; then
                echo "  replace  $skill"
                run rm -rf "$dest"
            else
                echo "  skip     $skill (exists and is not a symlink; use --force)"
                skipped=$((skipped + 1))
                continue
            fi
        else
            echo "  install  $skill"
        fi

        if [[ $mode == symlink ]]; then
            run ln -s "$src" "$dest"
        else
            run cp -R "$src" "$dest"
        fi
        installed=$((installed + 1))
    done
done

if [[ $dry_run == true ]]; then
    echo "dry run: $installed to install, $skipped skipped"
else
    echo "installed $installed, skipped $skipped"
fi

[[ $skipped -eq 0 ]]
