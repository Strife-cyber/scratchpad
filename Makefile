.PHONY: build build-cli run tidy clean test test-short test-race test-integration test-verbose test-coverage bench test-all lint

BINARY_NAME=scratchpad.exe
MCP_BINARY_NAME=scratchpad-mcp.exe
CLI_BINARY_NAME=scratchpad-cli.exe

# Build the binary (whole package, not a single file — config.go is a sibling)
build:
	go build -o $(BINARY_NAME) ./cmd/server

# Build the mcp binary
build-mcp:
	go build -o $(MCP_BINARY_NAME) ./cmd/mcp

# Build the CLI once, then drop the binary in another repo (no Go toolchain
# needed there). internal/testrunner can't be imported as a Go library from
# outside this module — the CLI, the REST/WS API, and sdk/ are the external
# surfaces.
build-cli:
	go build -o $(CLI_BINARY_NAME) ./cmd/cli

# Run the server (builds first)
run: build
	./$(BINARY_NAME)

# Clean up binaries and debug screenshots
clean:
	rm -f $(BINARY_NAME)
	rm -f $(CLI_BINARY_NAME)
	rm -f *.jpg
	rm -f *.png

# Tidy up Go modules
tidy:
	go mod tidy

# Run all tests
test:
	go test ./...

# Short tests (skip integration) with race detector
test-short:
	go test -short -race -count=1 ./...

# All tests with race detector
test-race:
	go test -race -count=1 ./...

# Integration tests against a real headless Chrome (fixture site + MCP
# conformance). The suites skip gracefully when Chrome is unavailable
# (SCRATCHPAD_SKIP_INTEGRATION, -short, or a failed probe), so this target is
# safe on browser-less machines but exercises the full loop in CI.
test-integration:
	go test -tags integration -count=1 ./internal/browser/ ./internal/mcp/

# Verbose test output
test-verbose:
	go test -v -count=1 ./...

# Test coverage report
test-coverage:
	go test -short -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html

# Benchmarks
bench:
	go test -bench=. -benchmem -count=3 ./...

# Everything: short tests + benchmarks
test-all: test-short bench

# Lint (requires golangci-lint)
lint:
	golangci-lint run ./...

# Documentation site (Starlight)
docs:
	cd docs && npm install

docs-dev: docs
	cd docs && npm run dev

docs-build: docs
	cd docs && npm run build

docs-preview: docs-build
	cd docs && npm run preview
