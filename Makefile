.PHONY: build run tidy clean test

BINARY_NAME=scratchpad.exe
MCP_BINARY_NAME=scratchpad-mcp.exe

# Build the binary
build:
		go build -o $(BINARY_NAME) cmd/server/main.go

# Build the mcp binary
build-mcp:
		go build -o $(MCP_BINARY_NAME) cmd/mcp/main.go

# Run the server (builds first)
run: build
		./$(BINARY_NAME)

# Clean up binaries and debug screenshots
clean:
		rm -f $(BINARY_NAME)
		rm -f *.jpg
		rm -f *.png

# Tidy up Go modules
tidy:
		go mod tidy

# Run all tests (useful as more logic is added)
test:
		go test ./...

# Documentation site (Starlight)
docs:
	cd docs && npm install

docs-dev: docs
	cd docs && npm run dev

docs-build: docs
	cd docs && npm run build

docs-preview: docs-build
	cd docs && npm run preview
