package testrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrLintFailed signals that a suite failed schema validation. The CLI maps it
// to a nonzero exit code.
var ErrLintFailed = errors.New("lint: suite failed validation")

// ErrInitExists signals that the init target already has content and --force
// was not given.
var ErrInitExists = errors.New("init: target already exists (use --force to overwrite)")

type LintOptions struct {
	InputPath string
	JSON      bool
}

type LintResult struct {
	Path          string        `json:"path"`
	OK            bool          `json:"ok"`
	Suites        int           `json:"suites,omitempty"`
	Steps         int           `json:"steps,omitempty"`
	Errors        []SchemaError `json:"errors,omitempty"`
	SchemaVersion string        `json:"schema_version,omitempty"`
}

// RunLint parses and schema-validates a suite file, reporting every problem
// with line numbers. It returns ErrLintFailed when validation fails so the CLI
// can exit nonzero.
func RunLint(opts LintOptions) error {
	if strings.TrimSpace(opts.InputPath) == "" {
		return errors.New("missing -i <suite.yml|suite.json>")
	}

	errs, err := ValidateSuiteYAMLFile(opts.InputPath)
	if err != nil {
		return err
	}

	res := LintResult{
		Path:          opts.InputPath,
		OK:            len(errs) == 0,
		SchemaVersion: "draft-07",
	}

	if res.OK {
		suites, steps := countSuitesAndSteps(opts.InputPath)
		res.Suites = suites
		res.Steps = steps
	} else {
		res.Errors = errs
	}

	if opts.JSON {
		data, marshalErr := json.MarshalIndent(res, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Fprintln(os.Stdout, string(data))
	} else {
		if res.OK {
			fmt.Fprintf(os.Stdout, "%s: valid (%d suite(s), %d step(s))\n", opts.InputPath, res.Suites, res.Steps)
		} else {
			fmt.Fprintf(os.Stderr, "%s: %d error(s)\n", opts.InputPath, len(errs))
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e.Error())
			}
		}
	}

	if !res.OK {
		return ErrLintFailed
	}
	return nil
}

// countSuitesAndSteps loads an already-validated suite file and counts suites
// and steps for the lint summary.
func countSuitesAndSteps(inputPath string) (int, int) {
	suites, err := loadSuites(inputPath)
	if err != nil {
		return 0, 0
	}
	steps := 0
	for _, s := range suites {
		steps += len(s.Steps)
	}
	return len(suites), steps
}
