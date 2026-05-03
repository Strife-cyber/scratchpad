package main

import (
	"log"
	"scratchpad/internal/mcp"

	mcpg "github.com/metoro-io/mcp-golang"
	"github.com/metoro-io/mcp-golang/transport/stdio" // Important: Import the stdio transport
)

func main() {
	engineURL := "ws://localhost:8080/ws"

	// 1. Connect the adapter to your engine
	adapter, err := mcp.NewMcpServer(engineURL)
	if err != nil {
		log.Fatalf("Engine connection failed: %v", err)
	}

	// 2. Initialize the Server with the Stdio Transport and Options
	s := mcpg.NewServer(
		stdio.NewStdioServerTransport(),
		mcpg.WithName("Browser-Engine-MCP"),
		mcpg.WithVersion("1.0.0"),
	)

	// 3. Register your tools
	adapter.RegisterTools(s)

	// 4. Start serving (Change Fatalf to Println!)
	log.Println("MCP Server active and waiting for JSON-RPC commands...")

	if err := s.Serve(); err != nil {
		log.Fatalf("MCP server failed: %v", err)
	}

	// 5. Keep the main goroutine alive (s.Serve() is non-blocking)
	select {}
}
