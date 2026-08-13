# CI merge gate: `make fmt-check`, `make test`, and `make build` must pass.
BIN_DIR  = bin
BINARY   = $(BIN_DIR)/approach
VERSION_PACKAGE = github.com/approachcontrol/approach/internal/version
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(VERSION_PACKAGE).version=dev -X $(VERSION_PACKAGE).commit=$(COMMIT) -X $(VERSION_PACKAGE).date=$(DATE)

# `web/node_modules` is inside the repo tree and some npm packages ship Go
# files, which `./...` and `gofmt -l .` would otherwise pick up on any machine
# that has run `npm install`. CI checks out clean and never installs, so this
# only matters locally.
#
# `./...` is handled by the `ignore web/node_modules` directive in go.mod, which
# drops the directory during pattern matching rather than filtering it out of
# the results — see the comment there for why the difference matters. `gofmt`
# does not read go.mod, so fmt-check still excludes it below.
#
# `-co --exclude-standard` is tracked *plus* new-but-unignored files: a file you
# just wrote is the one most likely to be misformatted, and listing only tracked
# files would let it pass a gate the docs tell you to run before shipping.
# `--exclude-standard` is also what honours web/.gitignore, so node_modules
# stays out.
GO_FILES_CMD = git ls-files -zco --exclude-standard '*.go'

.PHONY: build test fmt-check run clean tidy

build:
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/approach

# The package list is built in the recipe, not in a `$(shell ...)` variable: a
# variable swallows `go list`'s exit status, so a package that fails to load
# would yield a *partial* list and the surviving packages would pass green.
test:
	@packages=$$(go list ./...) || exit 1; \
	if [ -z "$$packages" ]; then echo 'test: no Go packages found' >&2; exit 1; fi; \
	go test $$packages

# Filenames are never interpolated into shell source. Git allows `$(...)`, a
# space, and a newline in a path, and a name expanded into a recipe is program
# text: `$(id).go` would *run* `id`, and `a b.go` would silently become two
# unchecked words. So the list travels NUL-delimited from `git ls-files -z`
# through `xargs -0`, which hands each name to the next program as one argument
# without a shell ever parsing it.
#
# `--` then stops `gofmt` from reading those names as options. A leading `-` is
# legal in a Git path, and `-cpuprofile=x.go` is both a valid filename and a
# documented `gofmt` flag: without the terminator the gate writes a CPU profile
# to `x.go`, reports nothing, and passes green with the real file unchecked.
#
# The list is also filtered to paths that still exist. `git ls-files -c` reports
# the index, which still holds a file you deleted in the worktree but have not
# staged — the ordinary middle of removing one. Passing that path to `gofmt`
# fails with `lstat: no such file or directory`, breaking the gate the docs tell
# you to run before shipping, at the one moment you cannot act on it.
#
# An empty list is an explicit error, because `gofmt -l` with no arguments reads
# stdin rather than failing, and a non-git checkout should not pass silently.
fmt-check:
	@$(GO_FILES_CMD) | tr -d '\0' | grep -q . || { \
		echo 'fmt-check: no Go files found; run from a git checkout' >&2; \
		exit 1; \
	}; \
	unformatted=$$($(GO_FILES_CMD) \
		| xargs -0 sh -c 'for file in "$$@"; do if [ -f "$$file" ]; then printf "%s\0" "$$file"; fi; done' sh \
		| xargs -0 gofmt -l --) || exit 1; \
	if [ -n "$$unformatted" ]; then printf '%s\n' "$$unformatted"; exit 1; fi

run: build
	XDG_CONFIG_HOME="$(CURDIR)/.config" ./$(BINARY)

clean:
	rm -rf $(BIN_DIR)

tidy:
	go mod tidy
