.PHONY: build run tidy clean test

BINARY_NAME=scratchpad.exe

# Build the binary
build:
		go build -o $(BINARY_NAME) cmd/server/main.go

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
