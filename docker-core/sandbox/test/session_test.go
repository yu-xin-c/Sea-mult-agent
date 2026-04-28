package sandbox_test

import (
	"strings"
	"testing"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/sandbox"
)

func TestShellEscape_SimpleValue(t *testing.T) {
	got := sandbox.ExportShellEscape("hello")
	if got != "'hello'" {
		t.Errorf("expected 'hello', got %s", got)
	}
}

func TestShellEscape_Empty(t *testing.T) {
	got := sandbox.ExportShellEscape("")
	if got != "''" {
		t.Errorf("expected empty quotes, got %s", got)
	}
}

func TestShellEscape_WithSingleQuote(t *testing.T) {
	got := sandbox.ExportShellEscape("it's")
	expected := "'it'\\''s'"
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestShellEscape_WithSpecialChars(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"hello world"},
		{"$HOME"},
		{"`cmd`"},
		{"a;b"},
		{"a|b"},
		{"a&b"},
		{"a(b)"},
		{"a#b"},
		{"a<b>c"},
		{"a~b"},
	}
	for _, tt := range tests {
		got := sandbox.ExportShellEscape(tt.input)
		// All values should be single-quoted
		if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
			t.Errorf("shellEscape(%q) = %s, should be single-quoted", tt.input, got)
		}
	}
}

func TestFilterEchoLine(t *testing.T) {
	delimiter := "__SANDBOX_EOF_123__"
	input := "real output\necho \"__SANDBOX_EOF_123__:$?\"\nmore output"
	got := sandbox.ExportFilterEchoLine(input, delimiter)

	if strings.Contains(got, "echo") {
		t.Errorf("echo line should be filtered, got: %s", got)
	}
	if !strings.Contains(got, "real output") {
		t.Error("should preserve real output")
	}
	if !strings.Contains(got, "more output") {
		t.Error("should preserve more output")
	}
}

func TestFilterEchoLine_NoMatch(t *testing.T) {
	input := "line1\nline2\nline3"
	got := sandbox.ExportFilterEchoLine(input, "__SANDBOX_EOF_999__")
	if got != input {
		t.Errorf("should not filter anything, got: %s", got)
	}
}

func TestFilterExportEcho(t *testing.T) {
	input := "export FOO='bar'\nreal output\nexport BAZ='qux'"
	got := sandbox.ExportFilterExportEcho(input)

	if strings.Contains(got, "export FOO") {
		t.Error("should filter export FOO echo")
	}
	if strings.Contains(got, "export BAZ") {
		t.Error("should filter export BAZ echo")
	}
	if !strings.Contains(got, "real output") {
		t.Error("should preserve real output")
	}
}

func TestFilterExportEcho_NoExport(t *testing.T) {
	input := "line1\nline2"
	got := sandbox.ExportFilterExportEcho(input)
	if got != input {
		t.Error("should not filter when no export lines")
	}
}

func TestParseAndStoreExport_Basic(t *testing.T) {
	s := sandbox.NewTestableSession()
	s.ParseAndStoreExport("export FOO=bar")
	if s.EnvMap()["FOO"] != "bar" {
		t.Errorf("expected bar, got %s", s.EnvMap()["FOO"])
	}
}

func TestParseAndStoreExport_DoubleQuoted(t *testing.T) {
	s := sandbox.NewTestableSession()
	s.ParseAndStoreExport(`export FOO="hello world"`)
	if s.EnvMap()["FOO"] != "hello world" {
		t.Errorf("expected 'hello world', got %s", s.EnvMap()["FOO"])
	}
}

func TestParseAndStoreExport_SingleQuoted(t *testing.T) {
	s := sandbox.NewTestableSession()
	s.ParseAndStoreExport("export FOO='hello world'")
	if s.EnvMap()["FOO"] != "hello world" {
		t.Errorf("expected 'hello world', got %s", s.EnvMap()["FOO"])
	}
}

func TestParseAndStoreExport_MixedQuotes(t *testing.T) {
	s := sandbox.NewTestableSession()
	// Value starts with " and ends with ' — should NOT strip
	s.ParseAndStoreExport(`export FOO="it's`)
	if s.EnvMap()["FOO"] != `"it's` {
		t.Errorf("mismatched quotes should not be stripped, got %s", s.EnvMap()["FOO"])
	}
}

func TestParseAndStoreExport_NoEquals(t *testing.T) {
	s := sandbox.NewTestableSession()
	s.ParseAndStoreExport("export FOO")
	if _, ok := s.EnvMap()["FOO"]; ok {
		t.Error("should not store export without =")
	}
}

func TestParseAndStoreExport_ValueWithEquals(t *testing.T) {
	s := sandbox.NewTestableSession()
	s.ParseAndStoreExport("export URL=http://host:8080/path?a=1")
	if s.EnvMap()["URL"] != "http://host:8080/path?a=1" {
		t.Errorf("value with = should be preserved, got %s", s.EnvMap()["URL"])
	}
}

func TestStreamSplitter_PreservesEmptyLines(t *testing.T) {
	// Empty lines between content should be preserved
	content := "line1\n\nline2\n__SANDBOX_EOF_789__:0\n"
	reader := strings.NewReader(content)

	ss, err := sandbox.NewStreamSplitter(reader, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	go ss.Run()

	timeoutCh := make(chan struct{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		output, exitCode := ss.ReadUntilDelimiter("__SANDBOX_EOF_789__", timeoutCh)
		if exitCode != 0 {
			t.Errorf("expected exit code 0, got %d", exitCode)
		}
		// Check that empty line is preserved
		lines := strings.Split(output, "\n")
		foundEmpty := false
		for _, l := range lines {
			if l == "" {
				foundEmpty = true
				break
			}
		}
		if !foundEmpty {
			t.Errorf("empty lines should be preserved, got: %q", output)
		}
	}()

	<-done
}
