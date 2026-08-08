package testrunner

import (
	"encoding/json"
	"testing"
)

// FuzzParseSuites feeds arbitrary decoded YAML/JSON roots into the suite
// parser and asserts it never panics on malformed input. The real loader
// (loadSuites) decodes YAML into `any`; here the fuzz bytes are decoded to
// `any` with encoding/json, which yields the same shapes (map[string]any /
// []any) the parser must defend against. Seeds cover the realistic roots a
// suite file can take: an array of suites, a single suite map, empty arrays,
// and malformed/scalar roots.
func FuzzParseSuites(f *testing.F) {
	seeds := []string{
		`[]`,
		`[{"name":"smoke","steps":[{"navigate":{"url":"https://example.com"}}]}]`,
		`[{"name":"smoke","steps":[{"navigate":{"url":"https://example.com"}},{"assert":{"condition":"title_contains","expected":"Example"}}]}]`,
		`{"name":"single","steps":[{"click":{"selector":{"css":"#btn"}}}]}`,
		`{"name":"single","steps":[]}`,
		`{"name":"no-steps"}`,
		`null`,
		`"just a string"`,
		`42`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return // not a decodable root; nothing to parse
		}
		_, _ = parseSuites(decoded)
	})
}
