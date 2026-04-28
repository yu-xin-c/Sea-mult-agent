package checkpoint

import "testing"

func TestSanitizeNodeID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "node"},
		{"simple", "simple"},
		{"MixedCase", "mixedcase"},
		{"hello world", "hello-world"},
		{"node/with/slashes", "node-with-slashes"},
		{"   leading spaces   ", "leading-spaces"},
		{"!@#$%^&*()", "node"},
		{"already-valid-123", "already-valid-123"},
		{"混合 unicode 123", "unicode-123"},
	}

	for _, tc := range tests {
		got := sanitizeNodeID(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeNodeID(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}
