# CI merge gate: `make fmt-check`, `make test`, and `make build` must pass.
BIN_DIR  = bin
BINARY   = $(BIN_DIR)/approach
VERSION_PACKAGE = github.com/approachcontrol/approach/internal/version
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(VERSION_PACKAGE).version=dev -X $(VERSION_PACKAGE).commit=$(COMMIT) -X $(VERSION_PACKAGE).date=$(DATE)

# `web/node_modules` is inside the repo tree and some npm packages ship Go
# files, which `./...` and `gofmt -l .` would otherwise pick up on any machine
# that has run `npm install`. Both gate targets exclude it; CI checks out clean
# and never installs, so this only matters locally.
#
# `-co --exclude-standard` is tracked *plus* new-but-unignored files: a file you
# just wrote is the one most likely to be misformatted, and listing only tracked
# files would let it pass a gate the docs tell you to run before shipping.
# `--exclude-standard` is also what honours web/.gitignore, so node_modules
# stays out.
GO_FILES = $(shell git ls-files -co --exclude-standard '*.go')

.PHONY: build test fmt-check run clean tidy

build:
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/approach

# The package list is built in the recipe, not in a `$(shell ...)` variable: a
# variable swallows `go list`'s exit status, so a package that fails to load
# would yield a *partial* list and the surviving packages would pass green.
test:
	@packages=$$(go list ./...) || exit 1; \
	packages=$$(printf '%s\n' "$$packages" | grep -v '/node_modules/'); \
	if [ -z "$$packages" ]; then echo 'test: no Go packages found' >&2; exit 1; fi; \
	go test $$packages

# An empty file list would leave `gofmt -l` with no arguments, which reads stdin
# and hangs instead of failing, so a non-git checkout is an explicit error.
fmt-check:
	@files='$(GO_FILES)'; \
	if [ -z "$$files" ]; then \
		echo 'fmt-check: no Go files found; run from a git checkout' >&2; \
		exit 1; \
	fi; \
	unformatted=$$(gofmt -l $$files) || exit 1; \
	if [ -n "$$unformatted" ]; then echo "$$unformatted"; exit 1; fi

run: build
	XDG_CONFIG_HOME="$(CURDIR)/.config" ./$(BINARY)

clean:
	rm -rf $(BIN_DIR)

tidy:
	go mod tidy
