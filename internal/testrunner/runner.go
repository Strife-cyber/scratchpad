package testrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scratchpad/internal/protocol"

	"gopkg.in/yaml.v3"
)

type RunOptions struct {
	InputPath string
	ServerURL string
	Headless  bool
	Platform  string

	Parallel int
	Retries  int

	TimeoutMS int

	Format   string // json|junit|both
	OutPath  string
	JUnitOut string
}

type Suite struct {
	Name     string
	Platform string
	Steps    []Step
}

type SuiteResult struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Attempts    int    `json:"attempts"`
	DurationMS  int64  `json:"duration_ms"`
	FailureStep string `json:"failure_step,omitempty"`
	Error       string `json:"error,omitempty"`
}

type Report struct {
	StartedAtRFC3339 string        `json:"started_at"`
	DurationMS       int64         `json:"duration_ms"`
	Suites           []SuiteResult `json:"suites"`
}

type Step struct {
	RawKey   string
	RawValue any
}

func RunSuites(opts RunOptions) error {
	if opts.Parallel <= 0 {
		opts.Parallel = 1
	}
	if opts.TimeoutMS <= 0 {
		opts.TimeoutMS = 60000
	}
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Platform == "" {
		opts.Platform = "web"
	}

	suites, err := loadSuites(opts.InputPath)
	if err != nil {
		return err
	}
	if len(suites) == 0 {
		return errors.New("no suites found in input")
	}

	start := time.Now()
	ctx := context.Background()

	sem := make(chan struct{}, opts.Parallel)
	wg := sync.WaitGroup{}

	results := make([]SuiteResult, len(suites))

	for i := range suites {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results[i] = runSingleSuite(ctx, suites[i], opts)
		}()
	}

	wg.Wait()

	rep := Report{
		StartedAtRFC3339: time.Now().UTC().Format(time.RFC3339Nano),
		DurationMS:       time.Since(start).Milliseconds(),
		Suites:           results,
	}

	if opts.Format == "json" || opts.Format == "both" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		if opts.OutPath != "" {
			if err := os.WriteFile(opts.OutPath, data, 0o644); err != nil {
				return err
			}
		}
		fmt.Fprintln(os.Stdout, string(data))
	}

	if (opts.Format == "junit" || opts.Format == "both") && opts.JUnitOut != "" {
		junit, err := repToJUnit(rep)
		if err != nil {
			return err
		}
		if err := os.WriteFile(opts.JUnitOut, junit, 0o644); err != nil {
			return err
		}
	}

	return nil
}

func runSingleSuite(ctx context.Context, suite Suite, opts RunOptions) SuiteResult {
	start := time.Now()
	attempts := 0

	for {
		attempts++
		res := runAttempt(ctx, suite, opts)
		if res.Passed {
			res.Attempts = attempts
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		}
		if attempts > opts.Retries {
			res.Attempts = attempts
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func runAttempt(ctx context.Context, suite Suite, opts RunOptions) SuiteResult {
	sessionID := ""

	// Per-attempt HTTP timeout.
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(opts.TimeoutMS)*time.Millisecond)
	defer cancel()

	platform := opts.Platform
	if suite.Platform != "" {
		platform = suite.Platform
	}
	id, err := createSession(reqCtx, opts.ServerURL, opts.Headless, platform)
	if err != nil {
		return SuiteResult{Name: suite.Name, Passed: false, Error: err.Error()}
	}
	sessionID = id
	defer func() { _ = deleteSession(context.Background(), opts.ServerURL, sessionID) }()

	for _, step := range suite.Steps {
		if err := execStep(reqCtx, opts.ServerURL, sessionID, step); err != nil {
			// If assertion failed, step error should already be descriptive.
			return SuiteResult{
				Name:        suite.Name,
				Passed:      false,
				FailureStep: step.RawKey,
				Error:       err.Error(),
			}
		}
	}

	_ = deleteSession(context.Background(), opts.ServerURL, sessionID)
	return SuiteResult{Name: suite.Name, Passed: true}
}

func loadSuites(inputPath string) ([]Suite, error) {
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}

	var decoded any
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}

	// Supported inputs:
	// - [ {name:..., steps:[...]} , ... ]
	// - [ {navigate:...}, {wait:...}, ... ]  (single suite)
	// - {name:..., steps:[...]}
	return parseSuites(decoded)
}

func parseSuites(decoded any) ([]Suite, error) {
	switch root := decoded.(type) {
	case []any:
		if len(root) == 0 {
			return nil, nil
		}
		// Decide whether it's suite objects or steps.
		allHaveSteps := true
		for _, it := range root {
			m, ok := it.(map[string]any)
			if !ok {
				allHaveSteps = false
				break
			}
			if _, ok := m["steps"]; !ok {
				allHaveSteps = false
				break
			}
		}
		if allHaveSteps {
			var suites []Suite
			for i, it := range root {
				m := it.(map[string]any)
				name := fmt.Sprintf("suite-%d", i)
				if v, ok := m["name"].(string); ok && v != "" {
					name = v
				}
				platform := ""
				if v, ok := m["platform"].(string); ok {
					platform = v
				}
				stepsRaw, _ := m["steps"].([]any)
				steps, err := parseSteps(stepsRaw)
				if err != nil {
					return nil, err
				}
				suites = append(suites, Suite{Name: name, Platform: platform, Steps: steps})
			}
			return suites, nil
		}

		steps, err := parseSteps(root)
		if err != nil {
			return nil, err
		}
		return []Suite{{Name: filepath.Base("suite"), Steps: steps}}, nil

	case map[string]any:
		name := "suite"
		if v, ok := root["name"].(string); ok && v != "" {
			name = v
		}
		platform := ""
		if v, ok := root["platform"].(string); ok && v != "" {
			platform = v
		}
		stepsRaw, _ := root["steps"].([]any)
		steps, err := parseSteps(stepsRaw)
		if err != nil {
			return nil, err
		}
		return []Suite{{Name: name, Platform: platform, Steps: steps}}, nil
	default:
		return nil, fmt.Errorf("unsupported root type %T", decoded)
	}
}

func parseSteps(stepsRaw []any) ([]Step, error) {
	steps := make([]Step, 0, len(stepsRaw))
	for _, it := range stepsRaw {
		m, ok := it.(map[string]any)
		if !ok || len(m) == 0 {
			return nil, fmt.Errorf("each step must be an object with one key; got %T", it)
		}
		if len(m) != 1 {
			return nil, fmt.Errorf("each step must have exactly one key; got keys=%v", reflectKeys(m))
		}
		for k, v := range m {
			steps = append(steps, Step{RawKey: k, RawValue: v})
		}
	}
	return steps, nil
}

func reflectKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func execStep(ctx context.Context, serverURL, sessionID string, step Step) error {
	switch step.RawKey {
	case "navigate":
		url, ok := step.RawValue.(string)
		if !ok {
			return fmt.Errorf("navigate expects string url")
		}
		initReq := protocol.InitializeRequest{
			URL:      url,
			Viewport: protocol.Viewport{Width: 0, Height: 0},
		}
		obs := &protocol.ObservationResponse{}
		return postAction(ctx, serverURL, sessionID, initReq, obs)
	case "wait":
		return handleWait(ctx, serverURL, sessionID, step.RawValue)
	case "type":
		return handleType(ctx, serverURL, sessionID, step.RawValue)
	case "click":
		return handleClick(ctx, serverURL, sessionID, step.RawValue)
	case "assert":
		return handleAssert(ctx, serverURL, sessionID, step.RawValue)
	case "observe":
		obs := &protocol.ObservationResponse{}
		if err := postTypedObserve(ctx, serverURL, sessionID, obs); err != nil {
			return err
		}
		// Print all visible text from the spatial tree.
		fmt.Println("=== PAGE CONTENT ===")
		var printTree func(nodes []protocol.SpatialNode, depth int)
		printTree = func(nodes []protocol.SpatialNode, depth int) {
			for _, n := range nodes {
				indent := strings.Repeat("  ", depth)
				if n.Name != "" {
					fmt.Printf("%s[%s] %s\n", indent, n.Role, n.Name)
				}
				if n.Value != "" {
					fmt.Printf("%s  value: %q\n", indent, n.Value)
				}
				if len(n.Children) > 0 {
					printTree(n.Children, depth+1)
				}
			}
		}
		printTree(obs.SpatialTree, 0)
		fmt.Println("=== END PAGE CONTENT ===")
		return nil
	default:
		return fmt.Errorf("unsupported step key %q", step.RawKey)
	}
}

func createSession(ctx context.Context, serverURL string, headless bool, platform string) (string, error) {
	body := map[string]any{
		"headless": headless,
		"platform": platform,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/v1/sessions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("create session failed: %s", resp.Status)
	}

	var decoded map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	return decoded["sessionId"], nil
}

func deleteSession(ctx context.Context, serverURL, sessionID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, serverURL+"/api/v1/sessions/"+sessionID, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("delete session failed: %s", resp.Status)
	}
	return nil
}

func postAction(ctx context.Context, serverURL, sessionID string, payload any, obs *protocol.ObservationResponse) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/v1/sessions/"+sessionID+"/actions", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("action request failed: %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
	}
	return json.NewDecoder(resp.Body).Decode(obs)
}

func postTypedObserve(ctx context.Context, serverURL, sessionID string, obs *protocol.ObservationResponse) error {
	payload := map[string]any{
		"action": map[string]any{"type": "observe"},
	}
	return postAction(ctx, serverURL, sessionID, payload, obs)
}

func handleWait(ctx context.Context, serverURL, sessionID string, v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("wait expects object")
	}
	timeout := asInt(m["timeout"])
	sel := parseSelector(m["selector"])
	condition := asString(m["condition"])
	if condition == "" {
		if sel != nil {
			condition = "selector_visible"
		} else {
			condition = ""
		}
	}

	req := protocol.ActionRequest{
		Action:    protocol.ActionWait,
		TimeoutMS: timeout,
	}
	if sel != nil {
		req.Condition = condition
		req.Selector = sel
	}

	obs := &protocol.ObservationResponse{}
	if err := postAction(ctx, serverURL, sessionID, req, obs); err != nil {
		return err
	}
	return nil
}

func handleType(ctx context.Context, serverURL, sessionID string, v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("type expects object")
	}
	sel := parseSelector(m["selector"])
	text := asString(m["text"])
	timeout := asInt(m["timeout_ms"])
	if timeout == 0 {
		timeout = asInt(m["timeout"])
	}
	if sel == nil {
		return fmt.Errorf("type requires selector")
	}

	req := protocol.ActionRequest{
		Action:    protocol.ActionType,
		Text:      text,
		TimeoutMS: timeout,
		Selector:  sel,
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

func handleClick(ctx context.Context, serverURL, sessionID string, v any) error {
	timeout := 0
	var sel *protocol.Selector
	switch vv := v.(type) {
	case string:
		sel = &protocol.Selector{CSS: vv}
	case map[string]any:
		sel = parseSelector(vv["selector"])
		timeout = asInt(vv["timeout"])
	default:
		return fmt.Errorf("click expects string selector or object")
	}
	if sel == nil || sel.IsEmpty() {
		return fmt.Errorf("click requires selector")
	}
	req := protocol.ActionRequest{
		Action:    protocol.ActionClick,
		TimeoutMS: timeout,
		Selector:  sel,
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

func handleAssert(ctx context.Context, serverURL, sessionID string, v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("assert expects object")
	}

	sel := parseSelector(m["selector"])
	txt := asString(m["text"])
	contains := asBool(m["contains"])
	equals := asBool(m["equals"])
	matches := asBool(m["matches"])
	visible := asBool(m["visible"])
	exists := asBool(m["exists"])
	checked := asBool(m["checked"])

	a := &protocol.AssertionRequest{Selector: sel}

	// Priority: element assertions when booleans are present; otherwise text assertions.
	switch {
	case visible:
		a.Type = "element_visible"
	case exists:
		a.Type = "element_exists"
	case m != nil && m["checked"] != nil:
		a.Type = "element_checked"
		if checked {
			t := true
			a.Checked = &t
		} else {
			f := false
			a.Checked = &f
		}
	case contains:
		a.Type = "text_contains"
		a.Text = txt
	case matches:
		a.Type = "text_matches"
		a.Pattern = txt
	case equals || (!contains && !matches):
		a.Type = "text_equals"
		a.Text = txt
	default:
		return fmt.Errorf("unsupported assert shape")
	}

	req := protocol.ActionRequest{
		Action:    "assert",
		Assertion: a,
	}

	obs := &protocol.ObservationResponse{}
	if err := postAction(ctx, serverURL, sessionID, req, obs); err != nil {
		return err
	}
	if obs.AssertionResult == nil {
		return fmt.Errorf("assertion_result missing in response")
	}
	if !obs.AssertionResult.Success {
		return fmt.Errorf("assertion failed: %s", obs.AssertionResult.Message)
	}
	return nil
}

// parseSelector parses a selector value from YAML into a structured Selector.
// It accepts either a plain CSS string (legacy) or a map with keys:
// css, xpath, text, role, test_id, placeholder.
// Returns nil if the value is absent or empty.
func parseSelector(v any) *protocol.Selector {
	s := &protocol.Selector{}
	switch val := v.(type) {
	case string:
		if val == "" {
			return nil
		}
		s.CSS = val
	case map[string]any:
		if css, ok := val["css"].(string); ok {
			s.CSS = css
		}
		if xpath, ok := val["xpath"].(string); ok {
			s.XPath = xpath
		}
		if text, ok := val["text"].(string); ok {
			s.Text = text
		}
		if role, ok := val["role"].(string); ok {
			s.Role = role
		}
		if testID, ok := val["test_id"].(string); ok {
			s.TestID = testID
		}
		if placeholder, ok := val["placeholder"].(string); ok {
			s.Placeholder = placeholder
		}
		if s.IsEmpty() {
			return nil
		}
	default:
		return nil
	}
	return s
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		i := 0
		_, _ = fmt.Sscanf(t, "%d", &i)
		return i
	default:
		return 0
	}
}

func asBool(v any) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	default:
		return false
	}
}

func repToJUnit(rep Report) ([]byte, error) {
	// Simple JUnit XML emitter (no dependency).
	type testcase struct {
		Name    string
		Time    float64
		Failed  bool
		Message string
	}

	var buf bytes.Buffer
	total := len(rep.Suites)
	failures := 0
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	buf.WriteString("<testsuite ")
	buf.WriteString(fmt.Sprintf(`name="%s" tests="%d" failures="%d"`, "scratchpad", total, failures))
	buf.WriteString(">")
	for _, s := range rep.Suites {
		tc := testcase{
			Name: s.Name,
			Time: float64(s.DurationMS) / 1000.0,
		}
		if !s.Passed {
			tc.Failed = true
			tc.Message = s.Error
			failures++
		}
		buf.WriteString("<testcase ")
		buf.WriteString(fmt.Sprintf(`name="%s" time="%.3f"`, xmlEscape(tc.Name), tc.Time))
		if tc.Failed {
			buf.WriteString("<failure message=\"")
			buf.WriteString(xmlEscape(tc.Message))
			buf.WriteString("\"></failure>")
		}
		buf.WriteString("</testcase>")
	}
	buf.WriteString("</testsuite>")
	return buf.Bytes(), nil
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
