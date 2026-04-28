package sandbox

import (
	"context"
	"fmt"
)

// ExecResult 单次命令执行的返回结果
type ExecResult struct {
	RawOutput        string // 完整原始输出（推理轨道原文）
	InferenceSummary string // 经 Truncator 精简后的推理摘要
	ExitCode         int    // 退出码，0 = 成功
}

// Sandbox 对外暴露的核心对象
// 封装 Engine（生命周期）、Session（TTY 会话）、Truncator（日志瘦身）
type Sandbox struct {
	engine    *Engine
	session   *Session
	truncator *Truncator
	config    Config
}

// New 创建并启动一个完整的沙箱实例
func New(ctx context.Context, cfg Config) (*Sandbox, error) {
	// 1. 创建容器
	engine, err := NewEngine(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox engine: %w", err)
	}

	// 2. 建立 TTY 会话
	session, err := NewSession(ctx, engine, cfg.AuditLogDir, cfg.ExecTimeout)
	if err != nil {
		engine.Destroy(ctx)
		return nil, fmt.Errorf("failed to create sandbox session: %w", err)
	}

	return &Sandbox{
		engine:    engine,
		session:   session,
		truncator: NewTruncator(cfg.TruncateN, cfg.TruncateM),
		config:    cfg,
	}, nil
}

// Execute 在沙箱内执行命令，仅返回原始输出字符串
// 特别说明：为了兼容 executor.Sandbox 接口，如果 exit code != 0，将返回错误
func (s *Sandbox) Execute(ctx context.Context, command string) (string, error) {
	result, err := s.session.Execute(ctx, command)
	if err != nil {
		return "", err
	}

	if result.ExitCode != 0 {
		return result.RawOutput, fmt.Errorf("command failed with exit code %d", result.ExitCode)
	}
	return result.RawOutput, nil
}

// Engine 暴露底层 Engine（供 checkpoint 模块调用）
func (s *Sandbox) Engine() *Engine {
	return s.engine
}

// Close 优雅关闭沙箱，释放所有资源
func (s *Sandbox) Close() error {
	if s.session != nil {
		s.session.Close()
	}
	if s.engine != nil {
		return s.engine.Destroy(context.Background())
	}
	return nil
}

// ReattachSession 在回滚重建容器后重新建立 TTY 会话
// 由 checkpoint.Manager 在 Rollback 完成后调用
func (s *Sandbox) ReattachSession(ctx context.Context) error {
	// 关闭旧会话
	if s.session != nil {
		s.session.Close()
	}

	// 用新容器建立新会话
	session, err := NewSession(ctx, s.engine, s.config.AuditLogDir, s.config.ExecTimeout)
	if err != nil {
		return fmt.Errorf("failed to reattach session: %w", err)
	}
	s.session = session
	return nil
}
