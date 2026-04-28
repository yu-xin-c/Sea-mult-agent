package sandbox

import "time"

// Config 沙箱配置
type Config struct {
	Image       string            // 基础镜像，如 "ubuntu:22.04"
	Runtime     string            // 容器运行时，"runsc"(gVisor) 或 "runc"(默认Docker)
	WorkingDir  string            // 容器内工作目录
	AuditLogDir string            // 审计日志落盘目录（宿主机路径）
	GPUEnabled  bool              // 是否启用 GPU (nvproxy)
	MemoryLimit int64             // 内存限制（字节）
	CPUQuota    int64             // CPU 配额（微秒）
	ExecTimeout time.Duration     // 单条命令超时
	TruncateN   int               // Truncator 保留前 N 行
	TruncateM   int               // Truncator 保留后 M 行
	EnvVars     map[string]string // 初始注入的环境变量
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		Image:       "ubuntu:22.04",
		Runtime:     "runc",
		WorkingDir:  "/workspace",
		AuditLogDir: "/tmp/sandbox-audit",
		MemoryLimit: 2 * 1024 * 1024 * 1024, // 2GB
		CPUQuota:    200000,                  // 2 cores
		ExecTimeout: 5 * time.Minute,
		TruncateN:   20,
		TruncateM:   50,
		EnvVars:     make(map[string]string),
	}
}
