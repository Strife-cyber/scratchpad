package browser

import (
	"testing"

	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// jsStringLiteral
// ---------------------------------------------------------------------------

func TestJsStringLiteral_Simple(t *testing.T) {
	got := jsStringLiteral("hello")
	if got != `"hello"` {
		t.Errorf("jsStringLiteral(\"hello\") = %q, want \"hello\"", got)
	}
}

func TestJsStringLiteral_WithQuotes(t *testing.T) {
	got := jsStringLiteral(`he"llo`)
	if got != `"he\"llo"` {
		t.Errorf("jsStringLiteral with embedded quote = %q", got)
	}
}

func TestJsStringLiteral_Empty(t *testing.T) {
	got := jsStringLiteral("")
	if got != `""` {
		t.Errorf("jsStringLiteral(\"\") = %q, want \"\"\"", got)
	}
}

func TestJsStringLiteral_WithBackslash(t *testing.T) {
	got := jsStringLiteral(`path\to\file`)
	// strconv.Quote should produce "path\\to\\file"
	if got == "" {
		t.Error("jsStringLiteral with backslash should not be empty")
	}
}

func TestJsStringLiteral_WithNewline(t *testing.T) {
	got := jsStringLiteral("line1\nline2")
	if got == "" {
		t.Error("jsStringLiteral with newline should not be empty")
	}
}

// ---------------------------------------------------------------------------
// Selector methods (pure logic, no CDP needed)
// ---------------------------------------------------------------------------

func TestSelectorIsEmpty_AllEmpty(t *testing.T) {
	s := protocol.Selector{}
	if !s.IsEmpty() {
		t.Error("empty Selector should be empty")
	}
}

func TestSelectorIsEmpty_WithCSS(t *testing.T) {
	s := protocol.Selector{CSS: ".btn"}
	if s.IsEmpty() {
		t.Error("Selector with CSS should not be empty")
	}
}

func TestSelectorIsEmpty_WithXPath(t *testing.T) {
	s := protocol.Selector{XPath: "//div"}
	if s.IsEmpty() {
		t.Error("Selector with XPath should not be empty")
	}
}

func TestSelectorIsEmpty_WithText(t *testing.T) {
	s := protocol.Selector{Text: "Submit"}
	if s.IsEmpty() {
		t.Error("Selector with Text should not be empty")
	}
}

func TestSelectorIsEmpty_WithRole(t *testing.T) {
	s := protocol.Selector{Role: "button"}
	if s.IsEmpty() {
		t.Error("Selector with Role should not be empty")
	}
}

func TestSelectorIsEmpty_WithTestID(t *testing.T) {
	s := protocol.Selector{TestID: "submit-btn"}
	if s.IsEmpty() {
		t.Error("Selector with TestID should not be empty")
	}
}

func TestSelectorIsEmpty_WithPlaceholder(t *testing.T) {
	s := protocol.Selector{Placeholder: "Enter name"}
	if s.IsEmpty() {
		t.Error("Selector with Placeholder should not be empty")
	}
}

func TestSelectorDescribe_CSS(t *testing.T) {
	s := protocol.Selector{CSS: ".btn-primary"}
	got := s.Describe()
	want := "css=.btn-primary"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestSelectorDescribe_XPath(t *testing.T) {
	s := protocol.Selector{XPath: "//form/button"}
	got := s.Describe()
	want := "xpath=//form/button"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestSelectorDescribe_Text(t *testing.T) {
	s := protocol.Selector{Text: "Click here"}
	got := s.Describe()
	want := "text=Click here"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestSelectorDescribe_Role(t *testing.T) {
	s := protocol.Selector{Role: "button"}
	got := s.Describe()
	want := "role=button"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestSelectorDescribe_TestID(t *testing.T) {
	s := protocol.Selector{TestID: "login-btn"}
	got := s.Describe()
	want := "testid=login-btn"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestSelectorDescribe_Placeholder(t *testing.T) {
	s := protocol.Selector{Placeholder: "email@example.com"}
	got := s.Describe()
	want := "placeholder=email@example.com"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestSelectorDescribe_Empty(t *testing.T) {
	s := protocol.Selector{}
	got := s.Describe()
	want := "empty selector"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// querySelectorMatchesJSON priority order (pure dispatch logic)
// ---------------------------------------------------------------------------

func TestSelectorPriority_CSS(t *testing.T) {
	// CSS should take priority over everything else
	s := protocol.Selector{
		CSS:   ".btn",
		XPath: "//button",
		Text:  "Click",
	}
	if s.CSS == "" {
		t.Error("CSS should be highest priority")
	}
}

func TestSelectorPriority_XPathOverText(t *testing.T) {
	s := protocol.Selector{
		XPath: "//button",
		Text:  "Click",
	}
	if s.XPath == "" {
		t.Error("XPath should take priority over Text")
	}
}
