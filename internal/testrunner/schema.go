package testrunner

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

//go:embed schema.json
var suiteSchemaJSON []byte

// SuiteSchemaJSON returns the embedded JSON Schema document describing the
// suite format. It is documentation-grade: the runtime validator below is
// hand-rolled over yaml.v3 so it can report line/column positions.
func SuiteSchemaJSON() []byte {
	return suiteSchemaJSON
}

// SchemaError is a single suite validation problem with its source location.
type SchemaError struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// Error implements error using the standard `line:column` prefix so editors
// and CI can jump straight to the offending line.
func (e SchemaError) Error() string {
	loc := "?"
	if e.Line > 0 {
		loc = fmt.Sprintf("%d:%d", e.Line, e.Column)
	}
	if e.Path != "" {
		return fmt.Sprintf("%s: %s: %s", loc, e.Path, e.Message)
	}
	return fmt.Sprintf("%s: %s", loc, e.Message)
}

// suiteActions lists every step action the runner can execute.
var suiteActions = map[string]bool{
	"navigate": true,
	"wait":     true,
	"type":     true,
	"click":    true,
	"assert":   true,
	"observe":  true,
}

// stepOptionKeys are per-step modifiers that sit alongside the action key.
var stepOptionKeys = map[string]bool{
	"timeout":               true,
	"retries":               true,
	"screenshot_on_failure": true,
	"tag":                   true,
	"tags":                  true,
}

// selectorKeys are allowed inside a structured selector mapping.
var selectorKeys = map[string]bool{
	"css":         true,
	"xpath":       true,
	"text":        true,
	"role":        true,
	"test_id":     true,
	"placeholder": true,
}

// ValidateSuiteYAML parses and schema-validates a suite document. It returns
// (nil, nil) for valid input, or (errors, nil) listing every problem with its
// line/column position. A YAML parse failure returns (nil, err).
func ValidateSuiteYAML(data []byte) ([]SchemaError, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return []SchemaError{{Line: 1, Column: 1, Message: "empty suite document"}}, nil
	}
	root := doc.Content[0]
	var errs []SchemaError
	validateRoot(root, &errs)
	return errs, nil
}

// ValidateSuiteYAMLFile reads and validates a suite file from disk.
func ValidateSuiteYAMLFile(path string) ([]SchemaError, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ValidateSuiteYAML(data)
}

// --- root / structure -------------------------------------------------------

func validateRoot(n *yaml.Node, errs *[]SchemaError) {
	switch n.Kind {
	case yaml.MappingNode:
		validateSuite(n, errs, "$")
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			*errs = append(*errs, SchemaError{Line: n.Line, Column: n.Column, Message: "empty suite list"})
			return
		}
		allHaveSteps := true
		for _, item := range n.Content {
			if !hasStepsKey(item) {
				allHaveSteps = false
				break
			}
		}
		if allHaveSteps {
			for i, item := range n.Content {
				validateSuite(item, errs, fmt.Sprintf("suites[%d]", i))
			}
		} else {
			for i, item := range n.Content {
				validateStep(item, errs, fmt.Sprintf("steps[%d]", i))
			}
		}
	default:
		*errs = append(*errs, SchemaError{Line: n.Line, Column: n.Column, Message: "suite document must be a mapping (one suite) or a sequence of steps"})
	}
}

func hasStepsKey(n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == "steps" {
			return true
		}
	}
	return false
}

func validateSuite(n *yaml.Node, errs *[]SchemaError, path string) {
	if n.Kind != yaml.MappingNode {
		*errs = append(*errs, SchemaError{Line: n.Line, Column: n.Column, Path: path, Message: "suite must be a mapping"})
		return
	}
	var stepsNode *yaml.Node
	for i := 0; i < len(n.Content); i += 2 {
		key := n.Content[i]
		val := n.Content[i+1]
		kp := path + "." + key.Value
		switch key.Value {
		case "name":
			requireStringScalar(key, val, errs, kp)
		case "platform":
			requireStringScalar(key, val, errs, kp)
			if val.Kind == yaml.ScalarNode {
				switch val.Value {
				case "", "web", "android":
				default:
					*errs = append(*errs, SchemaError{Line: val.Line, Column: val.Column, Path: kp, Message: fmt.Sprintf("platform %q is not supported (allowed: web, android)", val.Value)})
				}
			}
		case "env":
			validateEnv(key, val, errs, kp)
		case "tags":
			validateTags(key, val, errs, kp)
		case "timeout", "retries":
			requireNonNegInt(key, val, errs, kp)
		case "screenshot_on_failure":
			requireBool(key, val, errs, kp)
		case "steps":
			stepsNode = val
		default:
			*errs = append(*errs, SchemaError{Line: key.Line, Column: key.Column, Path: path, Message: fmt.Sprintf("unknown suite key %q", key.Value)})
		}
	}
	if stepsNode == nil {
		*errs = append(*errs, SchemaError{Line: n.Line, Column: n.Column, Path: path, Message: "suite is missing the required key \"steps\""})
		return
	}
	if stepsNode.Kind != yaml.SequenceNode {
		*errs = append(*errs, SchemaError{Line: stepsNode.Line, Column: stepsNode.Column, Path: path + ".steps", Message: "\"steps\" must be a sequence"})
		return
	}
	for i, step := range stepsNode.Content {
		validateStep(step, errs, fmt.Sprintf("%s.steps[%d]", path, i))
	}
}

func validateStep(n *yaml.Node, errs *[]SchemaError, path string) {
	if n.Kind != yaml.MappingNode {
		*errs = append(*errs, SchemaError{Line: n.Line, Column: n.Column, Path: path, Message: "step must be a mapping with one action key"})
		return
	}
	action := ""
	var actionVal *yaml.Node
	for i := 0; i < len(n.Content); i += 2 {
		key := n.Content[i]
		val := n.Content[i+1]
		kp := fmt.Sprintf("%s.%s", path, key.Value)
		if suiteActions[key.Value] {
			if action != "" {
				*errs = append(*errs, SchemaError{Line: key.Line, Column: key.Column, Path: path, Message: fmt.Sprintf("step has multiple action keys (%q and %q)", action, key.Value)})
				continue
			}
			action = key.Value
			actionVal = val
			continue
		}
		if stepOptionKeys[key.Value] {
			validateStepOption(key, val, errs, kp)
			continue
		}
		*errs = append(*errs, SchemaError{Line: key.Line, Column: key.Column, Path: path, Message: fmt.Sprintf("unknown step key %q (not a supported action or step option)", key.Value)})
	}
	if action == "" {
		*errs = append(*errs, SchemaError{Line: n.Line, Column: n.Column, Path: path, Message: "step is missing an action key (one of: navigate, wait, type, click, assert, observe)"})
		return
	}
	validateAction(action, actionVal, errs, fmt.Sprintf("%s.%s", path, action))
}

func validateAction(action string, v *yaml.Node, errs *[]SchemaError, path string) {
	switch action {
	case "navigate":
		if !isStringScalar(v) {
			*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "navigate expects a string URL"})
			return
		}
		if v.Value == "" {
			*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "navigate URL must not be empty"})
		}
	case "observe":
		if !isNull(v) && !isEmptyMap(v) {
			*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "observe expects no value (use null or an empty mapping)"})
		}
	case "wait":
		validateWait(v, errs, path)
	case "type":
		validateType(v, errs, path)
	case "click":
		validateClick(v, errs, path)
	case "assert":
		validateAssert(v, errs, path)
	}
}

// --- per-action validation ---------------------------------------------------

func validateWait(v *yaml.Node, errs *[]SchemaError, path string) {
	if !isMapping(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "wait expects a mapping with selector/condition/timeout"})
		return
	}
	for i := 0; i < len(v.Content); i += 2 {
		key := v.Content[i]
		val := v.Content[i+1]
		kp := path + "." + key.Value
		switch key.Value {
		case "selector":
			validateSelector(key, val, errs, kp)
		case "condition":
			requireStringScalar(key, val, errs, kp)
		case "timeout":
			requireNonNegInt(key, val, errs, kp)
		default:
			*errs = append(*errs, SchemaError{Line: key.Line, Column: key.Column, Path: path, Message: fmt.Sprintf("unknown wait key %q", key.Value)})
		}
	}
}

func validateType(v *yaml.Node, errs *[]SchemaError, path string) {
	if !isMapping(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "type expects a mapping with selector and text"})
		return
	}
	hasSelector, hasText := false, false
	for i := 0; i < len(v.Content); i += 2 {
		key := v.Content[i]
		val := v.Content[i+1]
		kp := path + "." + key.Value
		switch key.Value {
		case "selector":
			validateSelector(key, val, errs, kp)
			hasSelector = true
		case "text":
			requireStringScalar(key, val, errs, kp)
			hasText = true
		case "timeout", "timeout_ms":
			requireNonNegInt(key, val, errs, kp)
		default:
			*errs = append(*errs, SchemaError{Line: key.Line, Column: key.Column, Path: path, Message: fmt.Sprintf("unknown type key %q", key.Value)})
		}
	}
	if !hasSelector {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "type requires a selector"})
	}
	if !hasText {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "type requires text"})
	}
}

func validateClick(v *yaml.Node, errs *[]SchemaError, path string) {
	if isStringScalar(v) {
		if v.Value == "" {
			*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "click selector must not be empty"})
		}
		return
	}
	if !isMapping(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "click expects a string selector or a mapping"})
		return
	}
	hasSelector := false
	for i := 0; i < len(v.Content); i += 2 {
		key := v.Content[i]
		val := v.Content[i+1]
		kp := path + "." + key.Value
		switch key.Value {
		case "selector":
			validateSelector(key, val, errs, kp)
			hasSelector = true
		case "timeout":
			requireNonNegInt(key, val, errs, kp)
		default:
			*errs = append(*errs, SchemaError{Line: key.Line, Column: key.Column, Path: path, Message: fmt.Sprintf("unknown click key %q", key.Value)})
		}
	}
	if !hasSelector {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "click requires a selector"})
	}
}

func validateAssert(v *yaml.Node, errs *[]SchemaError, path string) {
	if !isMapping(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "assert expects a mapping"})
		return
	}
	conditions := 0
	for i := 0; i < len(v.Content); i += 2 {
		key := v.Content[i]
		val := v.Content[i+1]
		kp := path + "." + key.Value
		switch key.Value {
		case "selector":
			validateSelector(key, val, errs, kp)
		case "text":
			requireStringScalar(key, val, errs, kp)
			conditions++
		case "visible", "exists", "checked", "contains", "matches", "equals":
			requireBool(key, val, errs, kp)
			conditions++
		default:
			*errs = append(*errs, SchemaError{Line: key.Line, Column: key.Column, Path: path, Message: fmt.Sprintf("unknown assert key %q", key.Value)})
		}
	}
	if conditions == 0 {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "assert requires at least one condition (visible, exists, checked, contains, matches, equals, or text)"})
	}
}

// --- shared validators -------------------------------------------------------

func validateSelector(key, v *yaml.Node, errs *[]SchemaError, path string) {
	if isStringScalar(v) {
		if v.Value == "" {
			*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "selector must not be empty"})
		}
		return
	}
	if !isMapping(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "selector must be a string or a mapping with one of css, xpath, text, role, test_id, placeholder"})
		return
	}
	filled := 0
	for i := 0; i < len(v.Content); i += 2 {
		skey := v.Content[i]
		sval := v.Content[i+1]
		if !selectorKeys[skey.Value] {
			*errs = append(*errs, SchemaError{Line: skey.Line, Column: skey.Column, Path: path, Message: fmt.Sprintf("unknown selector key %q (allowed: css, xpath, text, role, test_id, placeholder)", skey.Value)})
			continue
		}
		requireStringScalar(skey, sval, errs, path+"."+skey.Value)
		if isStringScalar(sval) && sval.Value != "" {
			filled++
		}
	}
	if filled == 0 {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "selector mapping must set at least one strategy (css, xpath, text, role, test_id, placeholder)"})
	}
}

func validateEnv(key, v *yaml.Node, errs *[]SchemaError, path string) {
	if !isMapping(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "\"env\" must be a mapping of secret name to value or ${VAR} reference"})
		return
	}
	for i := 0; i < len(v.Content); i += 2 {
		k := v.Content[i]
		val := v.Content[i+1]
		if !isStringScalar(k) {
			*errs = append(*errs, SchemaError{Line: k.Line, Column: k.Column, Path: path, Message: "env key must be a string"})
		}
		if !isStringScalar(val) {
			*errs = append(*errs, SchemaError{Line: val.Line, Column: val.Column, Path: path, Message: fmt.Sprintf("env value for %q must be a string (e.g. ${LOGIN_PASSWORD})", k.Value)})
		}
	}
}

func validateTags(key, v *yaml.Node, errs *[]SchemaError, path string) {
	switch v.Kind {
	case yaml.ScalarNode:
		requireStringScalar(key, v, errs, path)
	case yaml.SequenceNode:
		for i, tag := range v.Content {
			requireStringScalar(key, tag, errs, fmt.Sprintf("%s[%d]", path, i))
		}
	default:
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: "\"tags\" must be a string or a sequence of strings"})
	}
}

func validateStepOption(key, v *yaml.Node, errs *[]SchemaError, path string) {
	switch key.Value {
	case "timeout", "retries":
		requireNonNegInt(key, v, errs, path)
	case "screenshot_on_failure":
		requireBool(key, v, errs, path)
	case "tag":
		requireStringScalar(key, v, errs, path)
	case "tags":
		validateTags(key, v, errs, path)
	}
}

// --- scalar type checks ------------------------------------------------------

func requireStringScalar(key, v *yaml.Node, errs *[]SchemaError, path string) {
	if !isStringScalar(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: fmt.Sprintf("%q expects a string value (got %s)", key.Value, nodeTag(v))})
	}
}

func requireNonNegInt(key, v *yaml.Node, errs *[]SchemaError, path string) {
	if !isIntScalar(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: fmt.Sprintf("%q expects an integer value (got %s)", key.Value, nodeTag(v))})
		return
	}
	if n, err := strconv.Atoi(v.Value); err == nil && n < 0 {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: fmt.Sprintf("%q must not be negative", key.Value)})
	}
}

func requireBool(key, v *yaml.Node, errs *[]SchemaError, path string) {
	if !isBoolScalar(v) {
		*errs = append(*errs, SchemaError{Line: v.Line, Column: v.Column, Path: path, Message: fmt.Sprintf("%q expects a boolean value (got %s)", key.Value, nodeTag(v))})
	}
}

func nodeTag(n *yaml.Node) string {
	if n == nil {
		return "null"
	}
	switch n.Kind {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.AliasNode:
		return "alias"
	}
	if n.Tag != "" {
		return n.Tag
	}
	return "scalar"
}

// --- yaml node kind helpers ---------------------------------------------------

func isStringScalar(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!str" }
func isIntScalar(n *yaml.Node) bool    { return n.Kind == yaml.ScalarNode && n.Tag == "!!int" }
func isBoolScalar(n *yaml.Node) bool   { return n.Kind == yaml.ScalarNode && n.Tag == "!!bool" }
func isNull(n *yaml.Node) bool         { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }
func isMapping(n *yaml.Node) bool      { return n.Kind == yaml.MappingNode }
func isEmptyMap(n *yaml.Node) bool     { return n.Kind == yaml.MappingNode && len(n.Content) == 0 }
