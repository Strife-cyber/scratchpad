package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"scratchpad/internal/testrunner"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "mcp":
		// Phase 3.1 request: same CLI can run existing MCP bridge.
		testrunner.RunMcp(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli run -i <suite.yml|suite.json> [flags]")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli mcp [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli run -i tests/login.yml --parallel 4 --format junit")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli mcp --engine-url ws://localhost:8080/ws")
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var (
		inputPath  = fs.String("i", "", "path to test suite YAML or JSON")
		serverURL  = fs.String("server", "http://localhost:8080", "scratchpad server base URL")
		headless   = fs.Bool("headless", true, "override headless for session creation")
		platform   = fs.String("platform", "web", "platform target: web|android")
		parallel   = fs.Int("parallel", 1, "max parallel suite executions")
		retries    = fs.Int("retries", 0, "retries per suite on failure")
		timeoutMS  = fs.Int("timeout-ms", 60000, "per suite HTTP timeout (ms)")
		format     = fs.String("format", "json", "report format: json|junit|both")
		outPath    = fs.String("out", "", "optional output file for JSON report")
		junitPath  = fs.String("junit-out", "", "optional output file for JUnit XML")
		userAgents = fs.String("user-agent", "", "optional user-agent (reserved for future)")
	)

	_ = userAgents
	fs.Parse(args)

	if strings.TrimSpace(*inputPath) == "" {
		log.Fatal("missing -i <suite.yml|suite.json>")
	}

	opts := testrunner.RunOptions{
		InputPath: *inputPath,
		ServerURL: strings.TrimRight(*serverURL, "/"),
		Headless:  *headless,
		Platform:  strings.ToLower(strings.TrimSpace(*platform)),
		Parallel:  *parallel,
		Retries:   *retries,
		TimeoutMS: *timeoutMS,
		Format:    *format,
		OutPath:   *outPath,
		JUnitOut:  *junitPath,
	}
	if err := testrunner.RunSuites(opts); err != nil {
		log.Fatal(err)
	}
}
