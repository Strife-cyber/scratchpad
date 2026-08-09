package testrunner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRunLint_ValidNewVerbs(t *testing.T) {
	path := writeSuite(t, `
name: New Verbs
steps:
  - select_option: {selector: "#country", option_value: "CA"}
  - select_option: {selector: {css: "#country"}, option_text: "Canada"}
  - execute_js: "1+1"
  - execute_js: {js: "document.title"}
  - scroll: {direction: "down", amount: 200}
  - scroll: {selector: "#list", direction: "up"}
  - press_key: {key: "Enter"}
  - press_key: {key: "Tab", modifiers: {ctrl: true, shift: true}}
  - press_key_combo: {key: "a", ctrl: true}
  - check: {selector: "#agree"}
  - uncheck: {selector: "#agree"}
  - assert: {selector: "li", count: 3}
  - assert: {selector: "a", attr: "href", value: "https://x.example"}
  - assert: {selector: "a", attr: "href", value: "example", contains: true}
  - assert: {url: "example.com"}
  - assert: {url: "https://exact.example", equals: true}
  - assert: {title: "Welcome"}
  - assert: {no_console_errors: true}
`)
	if err := RunLint(LintOptions{InputPath: path}); err != nil {
		t.Fatalf("valid suite using the new verbs should pass: %v", err)
	}
}

func TestValidateSuite_MalformedNewVerbs(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{"select_option missing option", "steps:\n  - select_option: {selector: \"#x\"}\n", "option_value or option_text"},
		{"select_option missing selector", "steps:\n  - select_option: {option_value: \"x\"}\n", "requires a selector"},
		{"execute_js missing js", "steps:\n  - execute_js: {}\n", "\"js\""},
		{"scroll missing direction", "steps:\n  - scroll: {amount: 50}\n", "requires a direction"},
		{"scroll bad direction", "steps:\n  - scroll: {direction: \"diagonal\"}\n", "not supported"},
		{"press_key missing key", "steps:\n  - press_key: {modifiers: {ctrl: true}}\n", "requires a key"},
		{"press_key bad modifier", "steps:\n  - press_key: {key: \"x\", modifiers: {super: true}}\n", "unknown modifier"},
		{"press_key_combo missing key", "steps:\n  - press_key_combo: {ctrl: true}\n", "requires a key"},
		{"check missing selector", "steps:\n  - check: {}\n", "requires a selector"},
		{"uncheck missing selector", "steps:\n  - uncheck: {}\n", "requires a selector"},
		{"assert attr without value", "steps:\n  - assert: {selector: \"a\", attr: \"href\"}\n", "attr assertion requires a value"},
		{"assert value without attr", "steps:\n  - assert: {selector: \"a\", value: \"x\"}\n", "requires an attr assertion"},
		{"assert count wrong type", "steps:\n  - assert: {selector: \"li\", count: \"three\"}\n", "integer"},
		{"assert empty condition", "steps:\n  - assert: {selector: \"h1\"}\n", "at least one condition"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := mustValidate(t, tc.yaml)
			if len(errs) == 0 {
				t.Fatal("expected an error")
			}
			if !strings.Contains(errs[0].Message, tc.wantSub) {
				t.Errorf("message = %q, want substring %q", errs[0].Message, tc.wantSub)
			}
			if errs[0].Line == 0 {
				t.Errorf("expected a line number, got %+v", errs[0])
			}
		})
	}
}

func TestRunLint_MalformedNewVerbReturnsErrLintFailed(t *testing.T) {
	path := writeSuite(t, "steps:\n  - scroll: {direction: \"diagonal\"}\n")
	if err := RunLint(LintOptions{InputPath: path}); !errors.Is(err, ErrLintFailed) {
		t.Fatalf("err = %v, want ErrLintFailed", err)
	}
}
