package checkpoint

import "testing"

func TestSanitizeDockerRefComponent(t *testing.T) {
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
		{"mixed.dots_and-dashes", "mixed.dots_and-dashes"},
		{"混合 unicode 123", "unicode-123"},
	}

	for _, tc := range tests {
		got := sanitizeDockerRefComponent(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeDockerRefComponent(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}
