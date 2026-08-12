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
GO_PACKAGES = $(shell go list ./... | grep -v '/node_modules/')
GO_FILES    = $(shell git ls-files '*.go')

.PHONY: build test fmt-check run clean tidy

build:
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/approach

test:
	go test $(GO_PACKAGES)

fmt-check:
	@test -z "$$(gofmt -l $(GO_FILES))" || { gofmt -l $(GO_FILES); exit 1; }

run: build
	XDG_CONFIG_HOME="$(CURDIR)/.config" ./$(BINARY)

clean:
	rm -rf $(BIN_DIR)

tidy:
	go mod tidy
