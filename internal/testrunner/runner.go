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

	Format   string // json|junit|html|both
	OutPath  string
	JUnitOut string
	HTMLOut  string

	DryRun bool
	Tag    string

	// ScreenshotDir is where failure screenshots are written (default "reports").
	ScreenshotDir string
}

type Suite struct {
	Name                   string
	Platform               string
	Env                    map[string]string
	Tags                   []string
	TimeoutMS              int
	Retries                int
	ScreenshotOnFailure    bool
	ScreenshotOnFailureSet bool
	Steps                  []Step
}

type SuiteResult struct {
	Name           string `json:"name"`
	Passed         bool   `json:"passed"`
	Attempts       int    `json:"attempts"`
	DurationMS     int64  `json:"duration_ms"`
	FailureStep    string `json:"failure_step,omitempty"`
	Error          string `json:"error,omitempty"`
	PageURL        string `json:"page_url,omitempty"`
	ScreenshotPath string `json:"screenshot_path,omitempty"`
}

type Report struct {
	StartedAtRFC3339 string        `json:"started_at"`
	DurationMS       int64         `json:"duration_ms"`
	Suites           []SuiteResult `json:"suites"`
}

type Step struct {
	RawKey   string
	RawValue any

	// Per-step options (from the extended suite format).
	TimeoutMS              int
	Retries                int
	ScreenshotOnFailure    bool
	ScreenshotOnFailureSet bool
	Tag                    string
	Tags                   []string
	Heal                   bool
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
	if strings.TrimSpace(opts.InputPath) == "" {
		return errors.New("missing -i <suite.yml|suite.json>")
	}

	// Pre-flight: schema-validate the suite before touching a browser. Invalid
	// suites fail here with line numbers instead of mid-run.
	errs, err := ValidateSuiteYAMLFile(opts.InputPath)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e.Error())
		}
		fmt.Fprintf(os.Stderr, "suite validation failed: %d error(s) in %s\n", len(errs), opts.InputPath)
		return ErrLintFailed
	}

	if opts.DryRun {
		fmt.Fprintf(os.Stdout, "dry-run: %s is valid\n", opts.InputPath)
		return nil
	}

	suites, err := loadSuites(opts.InputPath)
	if err != nil {
		return err
	}
	if len(suites) == 0 {
		return errors.New("no suites found in input")
	}

	if opts.Tag != "" {
		suites, err = filterSuitesByTag(suites, opts.Tag)
		if err != nil {
			return err
		}
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

	isJSON := opts.Format == "json" || opts.Format == "both"
	isJUnit := opts.Format == "junit" || opts.Format == "both"
	isHTML := opts.Format == "html" || opts.Format == "both"

	if isJSON {
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

	if isJUnit && opts.JUnitOut != "" {
		junit, err := repToJUnit(rep)
		if err != nil {
			return err
		}
		if err := os.WriteFile(opts.JUnitOut, junit, 0o644); err != nil {
			return err
		}
	}

	if isHTML {
		htmlData, err := repToHTML(rep)
		if err != nil {
			return err
		}
		if opts.HTMLOut != "" {
			if err := os.WriteFile(opts.HTMLOut, htmlData, 0o644); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(os.Stdout, string(htmlData))
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
		if err := runStepWithRetries(reqCtx, opts.ServerURL, sessionID, step); err != nil {
			pageURL, screenshotPath := captureFailure(opts, sessionID, suite.Name, step)
			return SuiteResult{
				Name:           suite.Name,
				Passed:         false,
				FailureStep:    step.RawKey,
				Error:          err.Error(),
				PageURL:        pageURL,
				ScreenshotPath: screenshotPath,
			}
		}
	}

	_ = deleteSession(context.Background(), opts.ServerURL, sessionID)
	return SuiteResult{Name: suite.Name, Passed: true}
}

// runStepWithRetries runs a single step, honouring the step's per-step retries.
// A short pause between attempts lets transient conditions settle.
func runStepWithRetries(ctx context.Context, serverURL, sessionID string, step Step) error {
	var lastErr error
	for attempt := 0; attempt <= step.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := execStep(ctx, serverURL, sessionID, step); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// captureFailure records the page URL and a screenshot (when requested) for a
// failed step. It uses a fresh context so a timed-out step still gets captured.
func captureFailure(opts RunOptions, sessionID, suiteName string, step Step) (string, string) {
	if !step.ScreenshotOnFailure {
		return "", ""
	}
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pageURL := ""
	obs := &protocol.ObservationResponse{}
	if err := postTypedObserve(cctx, opts.ServerURL, sessionID, obs); err == nil && obs.PageInfo != nil {
		pageURL = obs.PageInfo.URL
	}

	data, err := fetchScreenshot(cctx, opts.ServerURL, sessionID)
	if err != nil {
		return pageURL, ""
	}
	dir := opts.ScreenshotDir
	if dir == "" {
		dir = "reports"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return pageURL, ""
	}
	path := filepath.Join(dir, sanitizeFileName(suiteName)+".png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return pageURL, ""
	}
	return pageURL, path
}

func fetchScreenshot(ctx context.Context, serverURL, sessionID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/v1/sessions/"+sessionID+"/screenshot?format=png", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("screenshot request failed: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "suite"
	}
	return b.String()
}

func filterSuitesByTag(suites []Suite, tag string) ([]Suite, error) {
	kept := suites[:0]
	skipped := 0
	for _, s := range suites {
		if suiteHasTag(s, tag) {
			kept = append(kept, s)
		} else {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stdout, "skipped %d suite(s) not tagged %q\n", skipped, tag)
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("no suites match tag %q", tag)
	}
	return kept, nil
}

func suiteHasTag(s Suite, tag string) bool {
	for _, t := range s.Tags {
		if t == tag {
			return true
		}
	}
	return false
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
				s, err := suiteFromMap(m, fmt.Sprintf("suite-%d", i))
				if err != nil {
					return nil, err
				}
				suites = append(suites, s)
			}
			return suites, nil
		}

		steps, err := parseSteps(root)
		if err != nil {
			return nil, err
		}
		steps = interpolateSteps(steps, map[string]string{})
		return []Suite{{Name: filepath.Base("suite"), Steps: steps}}, nil

	case map[string]any:
		s, err := suiteFromMap(root, "suite")
		if err != nil {
			return nil, err
		}
		return []Suite{s}, nil
	default:
		return nil, fmt.Errorf("unsupported root type %T", decoded)
	}
}

func suiteFromMap(m map[string]any, defName string) (Suite, error) {
	name := defName
	if v, ok := m["name"].(string); ok && v != "" {
		name = v
	}
	platform := ""
	if v, ok := m["platform"].(string); ok && v != "" {
		platform = v
	}
	env := resolveSuiteEnv(m["env"])
	tags := asStringSlice(m["tags"])
	timeout := asInt(m["timeout"])
	retries := asInt(m["retries"])
	screenshot := false
	screenshotSet := false
	if v, ok := m["screenshot_on_failure"]; ok {
		screenshot = asBool(v)
		screenshotSet = true
	}

	stepsRaw, _ := m["steps"].([]any)
	steps, err := parseSteps(stepsRaw)
	if err != nil {
		return Suite{}, err
	}
	steps = interpolateSteps(steps, env)
	for i := range steps {
		if steps[i].TimeoutMS == 0 {
			steps[i].TimeoutMS = timeout
		}
		if steps[i].Retries == 0 {
			steps[i].Retries = retries
		}
		if !steps[i].ScreenshotOnFailureSet && screenshotSet {
			steps[i].ScreenshotOnFailure = screenshot
			steps[i].ScreenshotOnFailureSet = true
		}
	}
	return Suite{
		Name:                   name,
		Platform:               platform,
		Env:                    env,
		Tags:                   tags,
		TimeoutMS:              timeout,
		Retries:                retries,
		ScreenshotOnFailure:    screenshot,
		ScreenshotOnFailureSet: screenshotSet,
		Steps:                  steps,
	}, nil
}

// resolveSuiteEnv maps the suite's env section, resolving ${VAR} references
// against the process environment. Values are never logged by the runner.
func resolveSuiteEnv(raw any) map[string]string {
	env := map[string]string{}
	m, ok := raw.(map[string]any)
	if !ok {
		return env
	}
	for k, v := range m {
		s := asString(v)
		if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
			name := s[2 : len(s)-1]
			env[k] = os.Getenv(name)
		} else {
			env[k] = s
		}
	}
	return env
}

func interpolateSteps(steps []Step, env map[string]string) []Step {
	for i := range steps {
		steps[i].RawValue = interpolateValue(steps[i].RawValue, env)
	}
	return steps
}

func interpolateValue(v any, env map[string]string) any {
	switch t := v.(type) {
	case string:
		return expandVars(t, env)
	case map[string]any:
		for k, val := range t {
			t[k] = interpolateValue(val, env)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = interpolateValue(val, env)
		}
		return t
	default:
		return v
	}
}

func expandVars(s string, env map[string]string) string {
	return os.Expand(s, func(name string) string {
		if v, ok := env[name]; ok {
			return v
		}
		return os.Getenv(name)
	})
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, it := range t {
			out = append(out, asString(it))
		}
		return out
	default:
		return nil
	}
}

func parseSteps(stepsRaw []any) ([]Step, error) {
	steps := make([]Step, 0, len(stepsRaw))
	for idx, it := range stepsRaw {
		m, ok := it.(map[string]any)
		if !ok || len(m) == 0 {
			return nil, fmt.Errorf("step %d: each step must be an object with an action key; got %T", idx+1, it)
		}
		var step Step
		actionFound := false
		for k, v := range m {
			if suiteActions[k] {
				if actionFound {
					return nil, fmt.Errorf("step %d: multiple action keys (%q and %q)", idx+1, step.RawKey, k)
				}
				step.RawKey = k
				step.RawValue = v
				actionFound = true
				continue
			}
			switch k {
			case "timeout":
				step.TimeoutMS = asInt(v)
			case "retries":
				step.Retries = asInt(v)
			case "screenshot_on_failure":
				step.ScreenshotOnFailure = asBool(v)
				step.ScreenshotOnFailureSet = true
			case "tag":
				step.Tag = asString(v)
			case "tags":
				step.Tags = asStringSlice(v)
			case "heal":
				step.Heal = asBool(v)
			default:
				return nil, fmt.Errorf("step %d: unknown key %q (not a supported action or step option)", idx+1, k)
			}
		}
		if !actionFound {
			return nil, fmt.Errorf("step %d: missing action key (one of: %s)", idx+1, suiteActionNames)
		}
		steps = append(steps, step)
	}
	return steps, nil
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
		return handleWait(ctx, serverURL, sessionID, step)
	case "type":
		return handleType(ctx, serverURL, sessionID, step)
	case "click":
		return handleClick(ctx, serverURL, sessionID, step)
	case "assert":
		return handleAssert(ctx, serverURL, sessionID, step.RawValue)
	case "select_option":
		return handleSelectOption(ctx, serverURL, sessionID, step)
	case "execute_js":
		return handleExecuteJS(ctx, serverURL, sessionID, step)
	case "scroll":
		return handleScroll(ctx, serverURL, sessionID, step)
	case "press_key":
		return handlePressKey(ctx, serverURL, sessionID, step)
	case "press_key_combo":
		return handlePressKeyCombo(ctx, serverURL, sessionID, step)
	case "check":
		return handleCheckUncheck(ctx, serverURL, sessionID, protocol.ActionCheck, step)
	case "uncheck":
		return handleCheckUncheck(ctx, serverURL, sessionID, protocol.ActionUncheck, step)
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

// parseSelector converts a raw YAML selector value (string or structured map)
// into a protocol.Selector. It returns nil when the value is empty/absent.
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
		if name, ok := val["name"].(string); ok {
			s.Name = name
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

func handleWait(ctx context.Context, serverURL, sessionID string, step Step) error {
	m, ok := step.RawValue.(map[string]any)
	if !ok {
		return fmt.Errorf("wait expects object")
	}
	timeout := asInt(m["timeout"])
	if timeout == 0 {
		timeout = step.TimeoutMS
	}
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
		Heal:      step.Heal,
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

func handleType(ctx context.Context, serverURL, sessionID string, step Step) error {
	m, ok := step.RawValue.(map[string]any)
	if !ok {
		return fmt.Errorf("type expects object")
	}
	sel := parseSelector(m["selector"])
	text := asString(m["text"])
	timeout := asInt(m["timeout_ms"])
	if timeout == 0 {
		timeout = asInt(m["timeout"])
	}
	if timeout == 0 {
		timeout = step.TimeoutMS
	}
	if sel == nil {
		return fmt.Errorf("type requires selector")
	}

	req := protocol.ActionRequest{
		Action:    protocol.ActionType,
		Text:      text,
		TimeoutMS: timeout,
		Selector:  sel,
		Heal:      step.Heal,
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

func handleClick(ctx context.Context, serverURL, sessionID string, step Step) error {
	var sel *protocol.Selector
	timeout := 0
	switch vv := step.RawValue.(type) {
	case string:
		sel = parseSelector(vv)
	case map[string]any:
		sel = parseSelector(vv["selector"])
		timeout = asInt(vv["timeout"])
	default:
		return fmt.Errorf("click expects string selector or object")
	}
	if timeout == 0 {
		timeout = step.TimeoutMS
	}
	if sel == nil || sel.IsEmpty() {
		return fmt.Errorf("click requires selector")
	}
	req := protocol.ActionRequest{
		Action:    protocol.ActionClick,
		TimeoutMS: timeout,
		Selector:  sel,
		Heal:      step.Heal,
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

	a := &protocol.AssertionRequest{Selector: sel}

	// Precompute the explicit condition keys so the switch below stays
	// expression-only (Go switch cases do not allow an init statement).
	_, hasCount := m["count"]
	attr := asString(m["attr"])
	url := asString(m["url"])
	title := asString(m["title"])
	_, hasNoConsoleErrors := m["no_console_errors"]
	_, hasState := m["state"]

	// Priority: the newer explicit condition keys first (state, count, attr,
	// url, title, no_console_errors), then element assertions, then text
	// assertions — matching the order the conditions are documented in the
	// schema.
	switch {
	case hasState:
		sm, ok := m["state"].(map[string]any)
		if !ok {
			return fmt.Errorf("state assertion expects object")
		}
		if ds, ok := sm["document_status"].(string); ok && ds != "" {
			a.Type = "document_status"
			a.Value = ds
		} else if ir, ok := sm["inflight_requests"]; ok {
			n := asInt(ir)
			if n < 0 {
				return fmt.Errorf("state.inflight_requests must be a non-negative integer")
			}
			a.Type = "inflight_requests"
			a.Value = fmt.Sprintf("%d", n)
		} else {
			return fmt.Errorf("state assertion requires document_status or inflight_requests")
		}
	case hasCount:
		if sel == nil || sel.IsEmpty() {
			return fmt.Errorf("count assertion requires a selector")
		}
		a.Type = "element_count"
		a.ExpectedCount = asInt(m["count"])
	case attr != "":
		if sel == nil || sel.IsEmpty() {
			return fmt.Errorf("attr assertion requires a selector")
		}
		a.Type = "attr_equals"
		if contains {
			a.Type = "attr_contains"
		}
		a.Attribute = attr
		a.Value = asString(m["value"])
	case url != "":
		if equals {
			// Exact URL match. The engine has no "url_equals" type; page_url
			// with Value performs an exact comparison against location.href.
			a.Type = "page_url"
			a.Value = url
		} else {
			a.Type = "url_matches"
			a.Pattern = url
		}
	case title != "":
		a.Type = "page_title"
		a.Text = title
	case hasNoConsoleErrors:
		a.Type = "console_error_count"
		a.Value = "0"
	case visible:
		a.Type = "element_visible"
	case exists:
		a.Type = "element_exists"
	case m["checked"] != nil:
		a.Type = "element_checked"
		c := asBool(m["checked"])
		a.Checked = &c
	case contains:
		a.Type = "text_contains"
		a.Text = txt
	case matches:
		a.Type = "text_matches"
		a.Pattern = txt
	case equals || txt != "":
		a.Type = "text_equals"
		a.Text = txt
	default:
		return fmt.Errorf("unsupported assert shape")
	}

	req := protocol.ActionRequest{
		Action:    protocol.ActionAssert,
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

func handleSelectOption(ctx context.Context, serverURL, sessionID string, step Step) error {
	m, ok := step.RawValue.(map[string]any)
	if !ok {
		return fmt.Errorf("select_option expects object")
	}
	sel := parseSelector(m["selector"])
	if sel == nil || sel.IsEmpty() {
		return fmt.Errorf("select_option requires selector")
	}
	req := protocol.ActionRequest{
		Action:      protocol.ActionSelectOption,
		Selector:    sel,
		OptionValue: asString(m["option_value"]),
		OptionText:  asString(m["option_text"]),
		TimeoutMS:   resolveStepTimeout(m, step),
		Heal:        step.Heal,
	}
	if req.OptionValue == "" && req.OptionText == "" {
		return fmt.Errorf("select_option requires option_value or option_text")
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

func handleExecuteJS(ctx context.Context, serverURL, sessionID string, step Step) error {
	js := ""
	switch vv := step.RawValue.(type) {
	case string:
		js = vv
	case map[string]any:
		js = asString(vv["js"])
	default:
		return fmt.Errorf("execute_js expects a string or an object with \"js\"")
	}
	if strings.TrimSpace(js) == "" {
		return fmt.Errorf("execute_js requires js")
	}
	req := protocol.ActionRequest{
		Action: protocol.ActionExecuteJS,
		JS:     js,
	}
	obs := &protocol.ObservationResponse{}
	if err := postAction(ctx, serverURL, sessionID, req, obs); err != nil {
		return err
	}
	// Surface the JS return value when the engine captured one
	// (ActionMetadata["result"], see jsResultMetadata).
	if obs.ActionResult != nil && obs.ActionResult.ActionMetadata != nil {
		if raw, ok := obs.ActionResult.ActionMetadata["result"]; ok {
			if v, err := json.Marshal(raw); err == nil {
				fmt.Printf("execute_js result: %s\n", string(v))
			}
		}
	}
	return nil
}

func handleScroll(ctx context.Context, serverURL, sessionID string, step Step) error {
	m, ok := step.RawValue.(map[string]any)
	if !ok {
		return fmt.Errorf("scroll expects object")
	}
	direction := asString(m["direction"])
	amount := asInt(m["amount"])
	if amount == 0 {
		amount = 100 // default wheel tick
	}
	sel := parseSelector(m["selector"])
	req := protocol.ActionRequest{
		Action:    protocol.ActionScroll,
		Selector:  sel,
		TimeoutMS: resolveStepTimeout(m, step),
		Heal:      step.Heal,
	}
	// The engine dispatches a CDP MouseWheel event at the selector center (or
	// viewport centre) with DeltaX/DeltaY as the wheel deltas: positive DeltaY
	// scrolls down, positive DeltaX scrolls right.
	switch direction {
	case "up":
		req.DeltaY = -amount
	case "down":
		req.DeltaY = amount
	case "left":
		req.DeltaX = -amount
	case "right":
		req.DeltaX = amount
	default:
		return fmt.Errorf("scroll direction %q not supported (allowed: up, down, left, right)", direction)
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

func handlePressKey(ctx context.Context, serverURL, sessionID string, step Step) error {
	m, ok := step.RawValue.(map[string]any)
	if !ok {
		return fmt.Errorf("press_key expects object")
	}
	key := asString(m["key"])
	if key == "" {
		return fmt.Errorf("press_key requires key")
	}
	req := protocol.ActionRequest{
		Action:    protocol.ActionPressKey,
		Key:       key,
		TimeoutMS: resolveStepTimeout(m, step),
	}
	if mods, ok := m["modifiers"].(map[string]any); ok && len(mods) > 0 {
		req.Modifiers = &protocol.KeyboardModifiers{
			Alt:   asBool(mods["alt"]),
			Ctrl:  asBool(mods["ctrl"]),
			Meta:  asBool(mods["meta"]),
			Shift: asBool(mods["shift"]),
		}
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

func handlePressKeyCombo(ctx context.Context, serverURL, sessionID string, step Step) error {
	m, ok := step.RawValue.(map[string]any)
	if !ok {
		return fmt.Errorf("press_key_combo expects object")
	}
	key := asString(m["key"])
	if key == "" {
		return fmt.Errorf("press_key_combo requires key")
	}
	req := protocol.ActionRequest{
		Action: protocol.ActionPressKeyCombo,
		KeyChord: protocol.KeyChord{
			Key:   key,
			Ctrl:  asBool(m["ctrl"]),
			Alt:   asBool(m["alt"]),
			Shift: asBool(m["shift"]),
			Meta:  asBool(m["meta"]),
		},
		TimeoutMS: resolveStepTimeout(m, step),
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

func handleCheckUncheck(ctx context.Context, serverURL, sessionID string, action string, step Step) error {
	m, ok := step.RawValue.(map[string]any)
	if !ok {
		return fmt.Errorf("%s expects object", action)
	}
	sel := parseSelector(m["selector"])
	if sel == nil || sel.IsEmpty() {
		return fmt.Errorf("%s requires selector", action)
	}
	req := protocol.ActionRequest{
		Action:    action,
		Selector:  sel,
		TimeoutMS: resolveStepTimeout(m, step),
		Heal:      step.Heal,
	}
	obs := &protocol.ObservationResponse{}
	return postAction(ctx, serverURL, sessionID, req, obs)
}

// resolveStepTimeout returns the verb-level timeout_ms/timeout override, falling
// back to the step's (suite-inherited) timeout. Mirrors handleType/handleClick.
func resolveStepTimeout(m map[string]any, step Step) int {
	timeout := asInt(m["timeout_ms"])
	if timeout == 0 {
		timeout = asInt(m["timeout"])
	}
	if timeout == 0 {
		timeout = step.TimeoutMS
	}
	return timeout
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

// repToHTML renders the report as a self-contained HTML page. Failure rows
// include the observed page URL and, when captured, the screenshot.
func repToHTML(rep Report) ([]byte, error) {
	var buf bytes.Buffer
	total := len(rep.Suites)
	failures := 0
	for _, s := range rep.Suites {
		if !s.Passed {
			failures++
		}
	}
	buf.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	buf.WriteString("<title>Scratchpad Test Report</title>\n")
	buf.WriteString("<style>body{font-family:system-ui,sans-serif;margin:2rem}table{border-collapse:collapse;width:100%}th,td{border:1px solid #ddd;padding:8px;text-align:left;vertical-align:top}tr.pass td{background:#e6f4ea}tr.fail td{background:#fdecea}code{background:#f4f4f4;padding:2px 4px}img{max-width:240px;border:1px solid #ccc}</style>\n")
	buf.WriteString("</head>\n<body>\n")
	fmt.Fprintf(&buf, "<h1>Scratchpad Test Report</h1>\n")
	fmt.Fprintf(&buf, "<p>Started: <code>%s</code> · Duration: <code>%d ms</code> · Suites: <code>%d</code> · Failures: <code>%d</code></p>\n",
		xmlEscape(rep.StartedAtRFC3339), rep.DurationMS, total, failures)
	buf.WriteString("<table>\n<tr><th>Suite</th><th>Status</th><th>Duration (ms)</th><th>Failure step</th><th>Error</th><th>Page URL</th><th>Screenshot</th></tr>\n")
	for _, s := range rep.Suites {
		cls, status := "pass", "PASS"
		if !s.Passed {
			cls, status = "fail", "FAIL"
		}
		fmt.Fprintf(&buf, "<tr class=\"%s\"><td>%s</td><td>%s</td><td>%d</td>", cls, xmlEscape(s.Name), status, s.DurationMS)
		fmt.Fprintf(&buf, "<td>%s</td><td>%s</td>", xmlEscape(s.FailureStep), xmlEscape(s.Error))
		if s.PageURL != "" {
			fmt.Fprintf(&buf, "<td><a href=\"%s\">%s</a></td>", xmlEscape(s.PageURL), xmlEscape(s.PageURL))
		} else {
			buf.WriteString("<td></td>")
		}
		if s.ScreenshotPath != "" {
			fmt.Fprintf(&buf, "<td><img src=\"%s\" alt=\"failure screenshot\"></td>", xmlEscape(s.ScreenshotPath))
		} else {
			buf.WriteString("<td></td>")
		}
		buf.WriteString("</tr>\n")
	}
	buf.WriteString("</table>\n</body>\n</html>\n")
	return buf.Bytes(), nil
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
