package android

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// isASCIISafeType (pure)
// ---------------------------------------------------------------------------

func TestIsASCIISafeType(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"hello", true},
		{"aBc123", true},
		{"hello world", false},             // space
		{"héllo", false},                   // non-ASCII
		{"👋", false},                       // emoji
		{"line\nbreak", false},             // control char
		{"tab\there", false},               // tab
		{"50%", false},                     // input tool's %s escape char
		{"it's", false},                    // single quote
		{"say \"hi\"", false},              // double quote (also space)
		{"$HOME", false},                   // shell metachar
		{"a|b", false},                     // shell pipe
		{"", false},                        // empty
		{"hello, world!", false},           // space + punctuation
		{"UPPER.lower_123@test.com", true}, // safe punctuation allowed
	}
	for _, c := range cases {
		if got := isASCIISafeType(c.text); got != c.want {
			t.Errorf("isASCIISafeType(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// typeText — Unicode-safe type (item 32)
// ---------------------------------------------------------------------------

func TestTypeText_ASCII_NoEnterByDefault(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	if err := e.typeText(nil, "hello", false); err != nil {
		t.Fatalf("typeText: %v", err)
	}
	if !hasCall(f, "shell input text hello") {
		t.Errorf("calls = %v, want `shell input text hello`", f.calls)
	}
	if hasCall(f, "shell input keyevent 66") {
		t.Error("press_enter defaults to false: no ENTER should be sent")
	}
}

func TestTypeText_ASCII_PressEnter(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	if err := e.typeText(nil, "hello", true); err != nil {
		t.Fatalf("typeText: %v", err)
	}
	if !hasCall(f, "shell input keyevent 66") {
		t.Errorf("calls = %v, want ENTER after typing when press_enter=true", f.calls)
	}
}

func TestTypeText_NonASCII_UsesClipboardPaste(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	if err := e.typeText(nil, "héllo wörld", false); err != nil {
		t.Fatalf("typeText: %v", err)
	}
	if !hasCall(f, "shell cmd clipboard set-text héllo wörld") {
		t.Errorf("calls = %v, want clipboard set-text", f.calls)
	}
	if !hasCall(f, "shell input keyevent 279") {
		t.Errorf("calls = %v, want paste keyevent (279)", f.calls)
	}
	// The clipboard path must not attempt `input text` for non-ASCII.
	if hasCall(f, "shell input text héllo wörld") {
		t.Error("non-ASCII text must not go through `input text`")
	}
}

// ---------------------------------------------------------------------------
// clearText (item 32)
// ---------------------------------------------------------------------------

func TestClearText_SelectAllAndDelete(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	if err := e.clearText(nil); err != nil {
		t.Fatalf("clearText: %v", err)
	}
	if !hasCall(f, "shell input keyevent 123") { // MOVE_END
		t.Errorf("calls = %v, want MOVE_END (123)", f.calls)
	}
	if !hasCall(f, "shell input keycombination 113 29") { // CTRL+A
		t.Errorf("calls = %v, want CTRL+A select-all", f.calls)
	}
	if !hasCall(f, "shell input keyevent 67") { // DEL
		t.Errorf("calls = %v, want DEL (67)", f.calls)
	}
}

// errADB is a commandRunner that fails keycombination to exercise the
// pre-Android-11 hold-DEL fallback in clearText.
type errADB struct {
	*fakeADB
}

func (f *errADB) run(serial string, args ...string) (string, error) {
	out, err := f.fakeADB.run(serial, args...)
	for _, a := range args {
		if a == "keycombination" {
			return "", errors.New("keycombination unsupported")
		}
	}
	return out, err
}

func TestClearText_FallsBackToHoldDel(t *testing.T) {
	f := &errADB{fakeADB: &fakeADB{}}
	e := newAndroidEngineWithConn(newADBConn("", f))

	if err := e.clearText(nil); err != nil {
		t.Fatalf("clearText: %v", err)
	}
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, "keyevent --longpress 67") {
			n++
		}
	}
	if n == 0 {
		t.Errorf("calls = %v, want hold-DEL long-press fallback", f.calls)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func hasCall(f *fakeADB, substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}
