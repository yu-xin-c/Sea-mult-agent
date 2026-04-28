package sandbox

// testutil.go 导出内部函数的测试包装，供 sandbox/test 子目录使用。
// 这些函数仅用于测试目的。

// ExportShellEscape wraps shellEscape for external test packages.
func ExportShellEscape(s string) string {
	return shellEscape(s)
}

// ExportFilterEchoLine wraps filterEchoLine for external test packages.
func ExportFilterEchoLine(output, delimiter string) string {
	return filterEchoLine(output, delimiter)
}

// ExportFilterExportEcho wraps filterExportEcho for external test packages.
func ExportFilterExportEcho(output string) string {
	return filterExportEcho(output)
}

// TestableSession 封装 Session 的测试入口
type TestableSession struct {
	s *Session
}

// NewTestableSession 构造仅含 envMap 的 Session（无需 Docker 连接）
func NewTestableSession() *TestableSession {
	return &TestableSession{s: &Session{envMap: make(map[string]string)}}
}

// ParseAndStoreExport 调用内部 parseAndStoreExport
func (ts *TestableSession) ParseAndStoreExport(cmd string) {
	ts.s.parseAndStoreExport(cmd)
}

// EnvMap 返回内部 envMap
func (ts *TestableSession) EnvMap() map[string]string {
	return ts.s.envMap
}
