package testrunner

import (
	"flag"
	"log"
	"os"

	"scratchpad/internal/mcp"

	mcpg "github.com/metoro-io/mcp-golang"
	"github.com/metoro-io/mcp-golang/transport/stdio"
)

// RunMcp runs the existing MCP bridge logic so the same CLI binary can be
// used for AI workflows.
func RunMcp(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	defaultURL := "ws://localhost:8080/ws"
	if u := os.Getenv("SCRATCHPAD_URL"); u != "" {
		defaultURL = u
	}
	var (
		engineURL = fs.String("engine-url", defaultURL, "engine websocket URL (default $SCRATCHPAD_URL or ws://localhost:8080/ws)")
		name      = fs.String("name", "Browser-Engine-MCP", "mcp server name")
		version   = fs.String("version", "1.0.0", "mcp server version")
	)
	_ = fs.Parse(args)

	adapter, err := mcp.NewMcpServer(*engineURL)
	if err != nil {
		// Keep serving: the first tool call connects once the engine is up.
		log.Printf("engine not reachable yet; will connect on the first tool call: %v", err)
		adapter = mcp.NewLazyMcpServer(*engineURL)
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
