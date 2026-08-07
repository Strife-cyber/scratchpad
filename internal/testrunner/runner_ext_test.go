package testrunner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSteps_AllowsActionPlusOptions(t *testing.T) {
	steps, err := parseSteps([]any{
		map[string]any{
			"navigate":              "https://x.example",
			"timeout":               30000,
			"retries":               2,
			"screenshot_on_failure": true,
			"tag":                   "smoke",
		},
		map[string]any{
			"click": map[string]any{"selector": "#btn"},
			"tags":  []any{"smoke", "login"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(steps))
	}
	if steps[0].RawKey != "navigate" {
		t.Errorf("step0 key = %q, want navigate", steps[0].RawKey)
	}
	if steps[0].TimeoutMS != 30000 || steps[0].Retries != 2 {
		t.Errorf("step0 options = %+v", steps[0])
	}
	if !steps[0].ScreenshotOnFailure || !steps[0].ScreenshotOnFailureSet {
		t.Errorf("step0 screenshot_on_failure not parsed: %+v", steps[0])
	}
	if steps[0].Tag != "smoke" {
		t.Errorf("step0 tag = %q, want smoke", steps[0].Tag)
	}
	if len(steps[1].Tags) != 2 || steps[1].Tags[0] != "smoke" {
		t.Errorf("step1 tags = %v, want [smoke login]", steps[1].Tags)
	}
}

func TestParseSteps_MultipleActionKeysRejected(t *testing.T) {
	_, err := parseSteps([]any{
		map[string]any{
			"navigate": "https://x.example",
			"click":    "#btn",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple action keys") {
		t.Fatalf("err = %v, want multiple action keys error", err)
	}
}

func TestParseSteps_UnknownKeyRejected(t *testing.T) {
	_, err := parseSteps([]any{
		map[string]any{"hover": "#x"},
	})
	if err == nil || !strings.Contains(err.Error(), "hover") {
		t.Fatalf("err = %v, want unknown-key error", err)
	}
}

func TestParseSteps_MissingActionRejected(t *testing.T) {
	_, err := parseSteps([]any{
		map[string]any{"timeout": 1000},
	})
	if err == nil || !strings.Contains(err.Error(), "missing action key") {
		t.Fatalf("err = %v, want missing action key error", err)
	}
}

func TestSuiteFromMap_ExtendsSuite(t *testing.T) {
	s, err := suiteFromMap(map[string]any{
		"name":                  "Login",
		"platform":              "web",
		"tags":                  []any{"smoke"},
		"timeout":               5000,
		"retries":               3,
		"screenshot_on_failure": true,
		"env":                   map[string]any{"USER": "${LOGIN_USER}"},
		"steps": []any{
			map[string]any{"navigate": "https://x.example"},
		},
	}, "suite")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Login" || s.Platform != "web" {
		t.Errorf("suite = %+v", s)
	}
	if len(s.Tags) != 1 || s.Tags[0] != "smoke" {
		t.Errorf("tags = %v", s.Tags)
	}
	if len(s.Steps) != 1 {
		t.Fatalf("steps = %d", len(s.Steps))
	}
	// Suite-level defaults must cascade to the step.
	if s.Steps[0].TimeoutMS != 5000 {
		t.Errorf("step timeout = %d, want 5000", s.Steps[0].TimeoutMS)
	}
	if s.Steps[0].Retries != 3 {
		t.Errorf("step retries = %d, want 3", s.Steps[0].Retries)
	}
	if !s.Steps[0].ScreenshotOnFailure {
		t.Error("step screenshot_on_failure should inherit suite value")
	}
}

func TestResolveSuiteEnv_SecretReference(t *testing.T) {
	t.Setenv("LOGIN_USER", "alice")
	env := resolveSuiteEnv(map[string]any{
		"USER":  "${LOGIN_USER}",
		"FIXED": "plain",
	})
	if env["USER"] != "alice" {
		t.Errorf("USER = %q, want alice", env["USER"])
	}
	if env["FIXED"] != "plain" {
		t.Errorf("FIXED = %q, want plain", env["FIXED"])
	}
}

func TestExpandVars_UsesEnvThenProcessEnv(t *testing.T) {
	t.Setenv("HOME_VAR", "home")
	env := map[string]string{"USER": "alice"}
	got := expandVars("${USER}@${HOME_VAR}", env)
	if got != "alice@home" {
		t.Errorf("got %q, want alice@home", got)
	}
}

func TestInterpolateValue_Recursive(t *testing.T) {
	got := interpolateValue(map[string]any{
		"selector": map[string]any{"css": "#${ID}"},
		"text":     "hi ${NAME}",
	}, map[string]string{"ID": "btn", "NAME": "Bob"})
	m := got.(map[string]any)
	if m["text"] != "hi Bob" {
		t.Errorf("text = %v", m["text"])
	}
	sel := m["selector"].(map[string]any)
	if sel["css"] != "#btn" {
		t.Errorf("css = %v", sel["css"])
	}
}

func TestSuiteHasTag(t *testing.T) {
	s := Suite{Tags: []string{"smoke", "login"}}
	if !suiteHasTag(s, "login") {
		t.Error("expected login tag to match")
	}
	if suiteHasTag(s, "nightly") {
		t.Error("unexpected nightly tag match")
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"Login Flow":   "Login_Flow",
		"a/b:c*?":      "a_b_c__",
		"":             "suite",
		"already-ok.1": "already-ok.1",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAsStringSlice(t *testing.T) {
	if got := asStringSlice("smoke"); len(got) != 1 || got[0] != "smoke" {
		t.Errorf("single string = %v", got)
	}
	if got := asStringSlice([]any{"a", "b"}); len(got) != 2 || got[1] != "b" {
		t.Errorf("list = %v", got)
	}
	if got := asStringSlice(nil); got != nil {
		t.Errorf("nil = %v, want nil", got)
	}
}

func TestRepToHTML_ContainsPageURLAndScreenshot(t *testing.T) {
	rep := Report{
		StartedAtRFC3339: "2026-08-07T00:00:00Z",
		DurationMS:       42,
		Suites: []SuiteResult{
			{
				Name:           "Login",
				Passed:         false,
				FailureStep:    "click",
				Error:          "boom",
				PageURL:        "https://x.example/error",
				ScreenshotPath: "reports/Login.png",
			},
			{Name: "OK", Passed: true},
		},
	}
	html, err := repToHTML(rep)
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if !strings.Contains(s, "https://x.example/error") {
		t.Error("html missing page URL")
	}
	if !strings.Contains(s, "reports/Login.png") {
		t.Error("html missing screenshot reference")
	}
	if !strings.Contains(s, "FAIL") {
		t.Error("html missing FAIL status")
	}
}

func TestFilterSuitesByTag(t *testing.T) {
	suites := []Suite{
		{Name: "A", Tags: []string{"smoke"}},
		{Name: "B", Tags: []string{"nightly"}},
	}
	kept, err := filterSuitesByTag(suites, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Name != "A" {
		t.Errorf("kept = %+v", kept)
	}
	if _, err := filterSuitesByTag(suites, "nope"); err == nil {
		t.Error("expected error for no matching tag")
	}
}

func TestRunStepWithRetries_EventuallyPasses(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"action_result":{}}`))
	}))
	defer srv.Close()

	step := Step{RawKey: "wait", Retries: 3, RawValue: map[string]any{"selector": "h1"}}
	if err := runStepWithRetries(context.Background(), srv.URL, "sess", step); err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if calls < 2 {
		t.Errorf("calls = %d, want at least 2 (retries exercised)", calls)
	}
}

func TestRunStepWithRetries_ExhaustsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	step := Step{RawKey: "wait", Retries: 2, RawValue: map[string]any{"selector": "h1"}}
	err := runStepWithRetries(context.Background(), srv.URL, "sess", step)
	if err == nil {
		t.Fatal("expected failure after exhausting retries")
	}
}

func TestParseSuites_StepListName(t *testing.T) {
	suites, err := parseSuites([]any{
		map[string]any{"navigate": "https://x.example"},
		map[string]any{"observe": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) != 1 || len(suites[0].Steps) != 2 {
		t.Fatalf("got %+v", suites)
	}
}
