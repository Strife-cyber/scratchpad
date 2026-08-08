package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/go-json-experiment/json/jsontext"
)

// FuzzParseAXTree feeds arbitrary accessibility trees (derived from the fuzz
// bytes) into parseAXTree and asserts it never panics, including the
// boundsFromBackendNode lookup path (which fails fast on a context with no CDP
// executor). The decoder maps bytes to node properties so the corpus includes
// ignored nodes, missing roles, present/absent backend ids, and arbitrary name
// bytes that exercise both branches of axValueToString.
func FuzzParseAXTree(f *testing.F) {
	seeds := []byte{
		// 1 button node with a name: {count=1, ignored=0, backend=1, role=0(button), name="Click"}
		0, 0, 1, 0, 'C', 'l', 'i', 'c', 'k',
		// 1 ignored node: {count=1, ignored=1, backend=0, role=1(checkbox)}
		0, 1, 0, 1,
		// 3 nodes, no per-node bytes at all.
		2,
		// 1 generic node (role index out of range wraps to generic).
		0, 0, 255, 20,
	}
	for _, s := range seeds {
		f.Add([]byte{s})
	}
	f.Add([]byte{})
	f.Add([]byte{0})

	f.Fuzz(func(t *testing.T, data []byte) {
		(&ChromeEngine{}).parseAXTree(context.Background(), decodeAXNodes(data))
	})
}

// fuzzAXRoles is the set of roles parseAXTree recognizes as structural or
// interactive (see isStructuralOrInteractive) so generated nodes produce a
// non-trivial tree rather than being skipped as unknown.
var fuzzAXRoles = []string{
	"button", "checkbox", "link", "textbox", "menuitem", "option",
	"combobox", "listbox", "heading", "banner", "main", "article",
	"list", "listitem", "row", "cell", "generic", "group",
}

// decodeAXNodes derives a node list from the fuzz bytes. The first byte picks
// the node count (1-7); each node then consumes up to 16 bytes as property
// bands. Short or malformed inputs still yield valid (if degenerate) trees.
func decodeAXNodes(data []byte) []*accessibility.Node {
	if len(data) == 0 {
		return nil
	}
	count := int(data[0]%7) + 1
	rest := data[1:]
	nodes := make([]*accessibility.Node, 0, count)
	for i := 0; i < count && len(rest) > 0; i++ {
		chunk := rest
		rest = nil
		if len(chunk) > 16 {
			chunk, rest = chunk[:16], chunk[16:]
		}
		node := &accessibility.Node{NodeID: accessibility.NodeID(fmt.Sprintf("n%d", i))}
		if len(chunk) > 0 {
			node.Ignored = chunk[0]&1 == 1
		}
		if len(chunk) > 1 {
			node.BackendDOMNodeID = cdp.BackendNodeID(int64(chunk[1]))
		}
		role := "generic"
		if len(chunk) > 2 {
			role = fuzzAXRoles[int(chunk[2])%len(fuzzAXRoles)]
		}
		node.Role = axStringValue(role)
		if len(chunk) > 3 {
			// Name is JSON-encoded so it always parses via axValueToString.
			node.Name = axStringValue(string(chunk[3:]))
		}
		if len(chunk) > 4 {
			// Description is raw bytes: may be invalid JSON, exercising the
			// unquoted-identifier fallback in axValueToString.
			node.Description = &accessibility.Value{Value: jsontext.Value(chunk[4:])}
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// axStringValue wraps a string as a quoted-JSON AX value, the form Chrome
// emits for most accessible names and roles.
func axStringValue(s string) *accessibility.Value {
	b, _ := json.Marshal(s)
	return &accessibility.Value{Value: jsontext.Value(b)}
}
