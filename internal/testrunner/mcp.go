package testrunner

import (
	"flag"
	"log"

	"scratchpad/internal/mcp"

	mcpg "github.com/metoro-io/mcp-golang"
	"github.com/metoro-io/mcp-golang/transport/stdio"
)

// RunMcp runs the existing MCP bridge logic so the same CLI binary can be
// used for AI workflows.
func RunMcp(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	var (
		engineURL = fs.String("engine-url", "ws://localhost:8080/ws", "engine websocket URL")
		name      = fs.String("name", "Browser-Engine-MCP", "mcp server name")
		version   = fs.String("version", "1.0.0", "mcp server version")
	)
	_ = fs.Parse(args)

	adapter, err := mcp.NewMcpServer(*engineURL)
	if err != nil {
		log.Fatalf("Engine connection failed: %v", err)
	}

	s := mcpg.NewServer(
		stdio.NewStdioServerTransport(),
		mcpg.WithName(*name),
		mcpg.WithVersion(*version),
	)
	adapter.RegisterTools(s)

	log.Println("MCP Server active and waiting for JSON-RPC commands...")
	if err := s.Serve(); err != nil {
		log.Fatalf("MCP server failed: %v", err)
	}

	select {}
}
