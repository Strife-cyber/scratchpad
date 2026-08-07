package testrunner

import (
	"strings"
	"testing"
)

func mustValidate(t *testing.T, yamlDoc string) []SchemaError {
	t.Helper()
	errs, err := ValidateSuiteYAML([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	return errs
}

func TestValidateSuite_ValidSingleSuite(t *testing.T) {
	errs := mustValidate(t, `
name: "Navigation Test"
steps:
  - navigate: https://example.com
  - wait: {selector: "h1", timeout: 5000}
  - assert: {selector: "h1", text: "Example Domain", equals: true}
`)
	if len(errs) != 0 {
		t.Fatalf("expected valid suite, got %d errors: %v", len(errs), errs)
	}
}

func TestValidateSuite_ValidExtendedFormat(t *testing.T) {
	errs := mustValidate(t, `
name: "Login"
platform: web
tags: [smoke, login]
env:
  USER: ${LOGIN_USER}
  PASSWORD: ${LOGIN_PASSWORD}
steps:
  - navigate: https://example.com/login
    timeout: 30000
    retries: 2
    screenshot_on_failure: true
  - type:
      selector: {css: "#username", placeholder: "Username"}
      text: ${LOGIN_USER}
  - click: {selector: {test_id: "login-button"}}
    tag: smoke
  - assert: {selector: "body", exists: true}
`)
	if len(errs) != 0 {
		t.Fatalf("expected valid suite, got %d errors: %v", len(errs), errs)
	}
}

func TestValidateSuite_ValidStepListRoot(t *testing.T) {
	errs := mustValidate(t, `
- navigate: https://example.com
- wait: {selector: "h1"}
- observe: ~
`)
	if len(errs) != 0 {
		t.Fatalf("expected valid step list, got %d errors: %v", len(errs), errs)
	}
}

func TestValidateSuite_ValidSuiteListRoot(t *testing.T) {
	errs := mustValidate(t, `
- name: A
  steps:
    - navigate: https://a.example
- name: B
  steps:
    - observe: ~
`)
	if len(errs) != 0 {
		t.Fatalf("expected valid suite list, got %d errors: %v", len(errs), errs)
	}
}

func TestValidateSuite_UnknownStepKey(t *testing.T) {
	errs := mustValidate(t, `
steps:
  - hover: {selector: "#x"}
`)
	if len(errs) == 0 {
		t.Fatal("expected an error for unsupported step key")
	}
	if !strings.Contains(errs[0].Message, "hover") {
		t.Errorf("message = %q, want mention of hover", errs[0].Message)
	}
	if errs[0].Line == 0 {
		t.Errorf("expected a line number, got %+v", errs[0])
	}
}

func TestValidateSuite_MultipleActionKeys(t *testing.T) {
	errs := mustValidate(t, `
steps:
  - navigate: https://x.example
    click: "#btn"
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "multiple action keys") {
		t.Fatalf("expected multiple-action error, got %v", errs)
	}
}

func TestValidateSuite_MissingSteps(t *testing.T) {
	errs := mustValidate(t, `
name: "No Steps"
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "steps") {
		t.Fatalf("expected missing-steps error, got %v", errs)
	}
}

func TestValidateSuite_TypeError(t *testing.T) {
	errs := mustValidate(t, `
steps:
  - wait: {selector: "h1", timeout: "soon"}
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "integer") {
		t.Fatalf("expected integer-type error, got %v", errs)
	}
}

func TestValidateSuite_UnknownSuiteKey(t *testing.T) {
	errs := mustValidate(t, `
name: "X"
screenshot: true
steps:
  - observe: ~
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "screenshot") {
		t.Fatalf("expected unknown-suite-key error, got %v", errs)
	}
}

func TestValidateSuite_InvalidPlatform(t *testing.T) {
	errs := mustValidate(t, `
platform: ios
steps:
  - observe: ~
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "ios") {
		t.Fatalf("expected invalid-platform error, got %v", errs)
	}
}

func TestValidateSuite_EmptyDocument(t *testing.T) {
	errs := mustValidate(t, "\n")
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "empty") {
		t.Fatalf("expected empty-document error, got %v", errs)
	}
}

func TestValidateSuite_AssertWithoutCondition(t *testing.T) {
	errs := mustValidate(t, `
steps:
  - assert: {selector: "h1"}
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "condition") {
		t.Fatalf("expected missing-condition error, got %v", errs)
	}
}

func TestValidateSuite_InvalidSelectorKey(t *testing.T) {
	errs := mustValidate(t, `
steps:
  - click: {selector: {class: "btn"}}
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "class") {
		t.Fatalf("expected unknown-selector-key error, got %v", errs)
	}
}

func TestValidateSuite_LineNumbers(t *testing.T) {
	errs := mustValidate(t, "steps:\n  - hover: x\n")
	if len(errs) == 0 {
		t.Fatal("expected an error")
	}
	if errs[0].Line != 2 {
		t.Errorf("Line = %d, want 2", errs[0].Line)
	}
}

func TestSchemaError_ErrorFormat(t *testing.T) {
	e := SchemaError{Line: 4, Column: 3, Path: "steps[0].click", Message: "click requires a selector"}
	want := "4:3: steps[0].click: click requires a selector"
	if e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
}

func TestSuiteSchemaJSON_NonEmpty(t *testing.T) {
	if len(SuiteSchemaJSON()) == 0 {
		t.Fatal("embedded schema.json is empty")
	}
}
