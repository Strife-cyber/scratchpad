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
	case "doctor":
		doctorCmd(os.Args[2:])
	case "lint":
		lintCmd(os.Args[2:])
	case "init":
		initCmd(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "  scratchpad-cli lint -i <suite.yml|suite.json> [--json]")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli init [--dir DIR] [--force]")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli doctor [--fix] [--json] [--server URL] [--port N]")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli mcp [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli run -i tests/login.yml --parallel 4 --format junit")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli run -i tests/smoke.yml --tag smoke --dry-run")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli run -i tests/smoke.yml --report report.html --screenshots reports")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli lint -i tests/smoke.yml")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli init --dir tests")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli doctor --fix")
	fmt.Fprintln(os.Stderr, "  scratchpad-cli mcp --engine-url ws://localhost:8080/ws")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Exit codes:")
	fmt.Fprintln(os.Stderr, "  0  success (or lint passed / dry-run valid)")
	fmt.Fprintln(os.Stderr, "  1  any failure: doctor check failed, suite invalid, or a test suite failed")
	fmt.Fprintln(os.Stderr, "  doctor prints a fixed exit code of 1 when any check fails.")
}

func doctorCmd(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	var (
		serverURL = fs.String("server", "http://localhost:8080", "scratchpad server base URL")
		port      = fs.Int("port", 8080, "server port to probe for conflicts")
		docsDir   = fs.String("docs-dir", "docs", "documentation directory to check")
		fix       = fs.Bool("fix", false, "attempt to fix automatable failures (create missing dirs)")
		jsonOut   = fs.Bool("json", false, "emit a machine-readable JSON report")
	)
	_ = fs.Parse(args)

	opts := testrunner.DoctorOptions{
		ServerURL: strings.TrimRight(*serverURL, "/"),
		Port:      *port,
		DocsDir:   *docsDir,
		Fix:       *fix,
		JSON:      *jsonOut,
	}
	if err := testrunner.RunDoctor(opts); err != nil {
		os.Exit(1)
	}
}

func lintCmd(args []string) {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	var (
		inputPath = fs.String("i", "", "path to test suite YAML or JSON")
		jsonOut   = fs.Bool("json", false, "emit a machine-readable JSON result")
	)
	_ = fs.Parse(args)

	opts := testrunner.LintOptions{
		InputPath: *inputPath,
		JSON:      *jsonOut,
	}
	if err := testrunner.RunLint(opts); err != nil {
		os.Exit(1)
	}
}

func initCmd(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	var (
		dir   = fs.String("dir", ".", "directory to scaffold example suites into")
		force = fs.Bool("force", false, "overwrite existing files")
	)
	_ = fs.Parse(args)

	opts := testrunner.InitOptions{
		Dir:   *dir,
		Force: *force,
	}
	if err := testrunner.RunInit(opts); err != nil {
		log.Fatal(err)
	}
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var (
		inputPath   = fs.String("i", "", "path to test suite YAML or JSON")
		serverURL   = fs.String("server", "http://localhost:8080", "scratchpad server base URL")
		headless    = fs.Bool("headless", true, "override headless for session creation")
		platform    = fs.String("platform", "web", "platform target: web|android")
		parallel    = fs.Int("parallel", 1, "max parallel suite executions")
		retries     = fs.Int("retries", 0, "retries per suite on failure")
		timeoutMS   = fs.Int("timeout-ms", 60000, "per suite HTTP timeout (ms)")
		format      = fs.String("format", "json", "report format: json|junit|html|both")
		outPath     = fs.String("out", "", "optional output file for JSON report")
		junitPath   = fs.String("junit-out", "", "optional output file for JUnit XML")
		htmlOut     = fs.String("report", "", "optional output file for HTML report")
		dryRun      = fs.Bool("dry-run", false, "validate the suite and stop without executing")
		tag         = fs.String("tag", "", "only run suites carrying this tag")
		screenshots = fs.String("screenshots", "reports", "directory for failure screenshots (empty disables capture)")
		userAgents  = fs.String("user-agent", "", "optional user-agent (reserved for future)")
	)

	_ = userAgents
	fs.Parse(args)

	if strings.TrimSpace(*inputPath) == "" {
		log.Fatal("missing -i <suite.yml|suite.json>")
	}

	opts := testrunner.RunOptions{
		InputPath:     *inputPath,
		ServerURL:     strings.TrimRight(*serverURL, "/"),
		Headless:      *headless,
		Platform:      strings.ToLower(strings.TrimSpace(*platform)),
		Parallel:      *parallel,
		Retries:       *retries,
		TimeoutMS:     *timeoutMS,
		Format:        *format,
		OutPath:       *outPath,
		JUnitOut:      *junitPath,
		HTMLOut:       *htmlOut,
		DryRun:        *dryRun,
		Tag:           *tag,
		ScreenshotDir: *screenshots,
	}
	if err := testrunner.RunSuites(opts); err != nil {
		log.Fatal(err)
	}
}
