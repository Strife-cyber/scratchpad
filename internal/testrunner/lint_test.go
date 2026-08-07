package testrunner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeSuite(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "suite.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunLint_ValidSuite(t *testing.T) {
	path := writeSuite(t, "name: A\nsteps:\n  - navigate: https://x.example\n")
	if err := RunLint(LintOptions{InputPath: path}); err != nil {
		t.Fatalf("valid suite should pass: %v", err)
	}
}

func TestRunLint_InvalidSuiteReturnsErrLintFailed(t *testing.T) {
	path := writeSuite(t, "steps:\n  - hover: x\n")
	err := RunLint(LintOptions{InputPath: path})
	if !errors.Is(err, ErrLintFailed) {
		t.Fatalf("err = %v, want ErrLintFailed", err)
	}
}

func TestRunLint_MissingInput(t *testing.T) {
	if err := RunLint(LintOptions{}); err == nil {
		t.Fatal("expected an error for missing -i")
	}
}

func TestRunLint_JSONOutput(t *testing.T) {
	path := writeSuite(t, "name: A\nsteps:\n  - observe: ~\n")
	// JSON mode writes to stdout; just ensure no error and result is valid.
	if err := RunLint(LintOptions{InputPath: path, JSON: true}); err != nil {
		t.Fatalf("valid suite JSON lint should pass: %v", err)
	}
}

func TestCountSuitesAndSteps(t *testing.T) {
	path := writeSuite(t, `
- name: A
  steps:
    - navigate: https://a.example
    - wait: {selector: "h1"}
- name: B
  steps:
    - observe: ~
`)
	suites, steps := countSuitesAndSteps(path)
	if suites != 2 {
		t.Errorf("suites = %d, want 2", suites)
	}
	if steps != 3 {
		t.Errorf("steps = %d, want 3", steps)
	}
}
