package middleware

import "testing"

// TestSecureEqual: constant-time compare accepts only exact matches, and never
// matches an empty candidate (the guard that keeps a missing token from ever
// authenticating).
func TestSecureEqual(t *testing.T) {
	if !secureEqual("abc", "abc") {
		t.Error("equal strings should match")
	}
	if secureEqual("abc", "abd") {
		t.Error("differing strings should not match")
	}
	if secureEqual("abc", "abcd") {
		t.Error("different-length strings should not match")
	}
	if secureEqual("", "abc") || secureEqual("abc", "") || secureEqual("", "") {
		t.Error("empty candidates must never match")
	}
}
