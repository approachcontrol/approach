BIN_DIR  = bin
BINARY   = $(BIN_DIR)/wtui
VERSION_PACKAGE = github.com/brian-bell/wtui/internal/version
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(VERSION_PACKAGE).version=dev -X $(VERSION_PACKAGE).commit=$(COMMIT) -X $(VERSION_PACKAGE).date=$(DATE)

.PHONY: build test run clean tidy

build:
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/wtui

test:
	go test ./...

run: build
	./$(BINARY)

clean:
	rm -rf $(BIN_DIR)

tidy:
	go mod tidy
