package browser

import (
	"strings"
	"testing"
)

// TestClipboardWriteTextJS verifies the primary clipboard write path targets
// navigator.clipboard.writeText and carries the text as a correctly-escaped JS
// string literal.
func TestClipboardWriteTextJS(t *testing.T) {
	js := clipboardWriteTextJS("OTP-1234")
	if !strings.Contains(js, "navigator.clipboard.writeText") {
		t.Errorf("writeText JS missing navigator.clipboard.writeText: %s", js)
	}
	if !strings.Contains(js, `"OTP-1234"`) {
		t.Errorf("writeText JS missing escaped text: %s", js)
	}
	// A value with quotes/newlines must be escaped safely inside the literal.
	escaped := clipboardWriteTextJS(`say "hi"\nthere`)
	if !strings.Contains(escaped, `\"hi\"`) {
		t.Errorf("writeText JS did not escape quotes: %s", escaped)
	}
}

// TestClipboardFallbackJS verifies the document.execCommand fallback builders
// exist and use the hidden-textarea pattern (the pre-Async-Clipboard-API path
// for pages without clipboard permissions).
func TestClipboardFallbackJS(t *testing.T) {
	copyJS := clipboardCopyJS("secret-token")
	for _, want := range []string{"execCommand('copy')", "document.createElement('textarea')", "ta.select()"} {
		if !strings.Contains(copyJS, want) {
			t.Errorf("clipboardCopyJS missing %q: %s", want, copyJS)
		}
	}

	pasteJS := clipboardPasteJS()
	for _, want := range []string{"execCommand('paste')", "document.createElement('textarea')", "ta.value"} {
		if !strings.Contains(pasteJS, want) {
			t.Errorf("clipboardPasteJS missing %q: %s", want, pasteJS)
		}
	}
}

// TestClipboardTextEscaping verifies user text is embedded as a JS string
// literal so quotes and backslashes cannot break out of the injected script.
func TestClipboardTextEscaping(t *testing.T) {
	tricky := `a"b\c'd`
	js := clipboardWriteTextJS(tricky)
	// The literal must contain the escaped form, not a raw unescaped quote.
	if strings.Contains(js, `a"b`) {
		t.Errorf("writeText JS leaked an unescaped quote: %s", js)
	}
}
