package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHybridExample_StepPlatformTags verifies the hybrid web+android example
// (improvement-plan item 31) documents step-level platform tags. The file is
// parsed with plain yaml, not ValidateSuiteYAML: the step-level platform key is
// the intended future format and is not yet accepted by the strict validator,
// so this test only guards the example's shape (every step carries a valid
// web/android tag and an action key, and both contexts appear).
func TestHybridExample_StepPlatformTags(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "hybrid.yml"))
	if err != nil {
		t.Fatalf("read hybrid.yml: %v", err)
	}

	var steps []map[string]any
	if err := yaml.Unmarshal(data, &steps); err != nil {
		t.Fatalf("parse hybrid.yml: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("hybrid.yml has no steps")
	}

	seen := map[string]bool{}
	for i, step := range steps {
		plat, _ := step["platform"].(string)
		if plat == "" {
			t.Errorf("step %d: missing platform tag", i)
			continue
		}
		if plat != "web" && plat != "android" {
			t.Errorf("step %d: platform %q not one of web/android", i, plat)
		}
		seen[plat] = true
		foundAction := false
		for k := range step {
			if k != "platform" {
				foundAction = true
				break
			}
		}
		if !foundAction {
			t.Errorf("step %d: no action key alongside platform", i)
		}
	}
	if !seen["web"] {
		t.Error("hybrid.yml must include web steps")
	}
	if !seen["android"] {
		t.Error("hybrid.yml must include android steps")
	}
}
