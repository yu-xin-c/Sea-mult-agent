package checkpoint

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/sandbox"
)

// nonAlphanumRe matches any sequence of characters that are not lowercase ASCII letters or digits.
// Used by sanitizeNodeID to produce Docker-safe image reference components.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

// RollbackResult 回滚操作的结果
type RollbackResult struct {
	OldContainerID string // 被销毁的旧容器 ID
	NewContainerID string // 新拉起的容器 ID
	RestoredNodeID string // 回滚到的 DAG 节点 ID
	ImageID        string // 使用的快照镜像 ID
}

// Manager checkpoint 核心管理器
// 协调 Docker Commit / 容器重建 / 负反馈注入
type Manager struct {
	sandbox  *sandbox.Sandbox
	registry *SnapshotRegistry
	injector *FeedbackInjector
}

// NewManager 创建 checkpoint 管理器，绑定到指定沙箱
func NewManager(sb *sandbox.Sandbox) *Manager {
	return &Manager{
		sandbox:  sb,
		registry: NewSnapshotRegistry(),
		injector: NewFeedbackInjector(),
	}
}

// Commit 为当前容器状态创建快照
// 对齐 executor.CheckpointManager 接口，返回 ImageID
func (m *Manager) Commit(ctx context.Context, nodeID string) (string, error) {
	engine := m.sandbox.Engine()
	containerID := engine.ContainerID()
	if containerID == "" {
		return "", fmt.Errorf("no active container to commit")
	}

	ref := fmt.Sprintf("checkpoint-%s-%d", sanitizeNodeID(nodeID), time.Now().Unix())

	// Docker Commit：将当前容器文件系统 + 环境状态打包为新镜像
	resp, err := engine.Client().ContainerCommit(ctx, containerID,
		container.CommitOptions{
			Reference: ref,
			Comment:   fmt.Sprintf("Checkpoint for DAG node: %s", nodeID),
		},
	)
	if err != nil {
		return "", fmt.Errorf("docker commit failed for node %s: %w", nodeID, err)
	}

	snap := Snapshot{
		NodeID:    nodeID,
		ImageID:   resp.ID,
		CreatedAt: time.Now().Unix(),
		Comment:   ref,
	}
	m.registry.Register(snap)

	return resp.ID, nil
}

// Rollback 基于指定镜像 ID 执行回滚
// 对齐 executor.CheckpointManager 接口
func (m *Manager) Rollback(ctx context.Context, imageID string) error {
	engine := m.sandbox.Engine()

	// 基于快照镜像重启容器
	_, err := engine.RestartFrom(ctx, imageID)
	if err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	// 重新建立 TTY 会话
	if err := m.sandbox.ReattachSession(ctx); err != nil {
		return fmt.Errorf("rollback succeeded but session reattach failed: %w", err)
	}

	// 查找 imageID 对应的 nodeID，清理注册表
	for _, snap := range m.registry.All() {
		if snap.ImageID == imageID {
			m.registry.RollbackTo(snap.NodeID)
			break
		}
	}

	return nil
}

// RollbackToNode 回滚到指定 DAG 节点的快照
func (m *Manager) RollbackToNode(ctx context.Context, nodeID string) error {
	snap, ok := m.registry.GetByNodeID(nodeID)
	if !ok {
		return fmt.Errorf("no snapshot found for node: %s", nodeID)
	}

	return m.Rollback(ctx, snap.ImageID)
}

// RollbackToLatest 回滚到最近一次快照
func (m *Manager) RollbackToLatest(ctx context.Context) error {
	snap, ok := m.registry.GetLatest()
	if !ok {
		return fmt.Errorf("no snapshots available for rollback")
	}
	return m.RollbackToNode(ctx, snap.NodeID)
}

// RecordFailureAndGetFeedback 记录失败并返回负反馈提示词
func (m *Manager) RecordFailureAndGetFeedback(nodeID, command, stderr string) string {
	m.injector.RecordFailure(FailureRecord{
		NodeID:    nodeID,
		Command:   command,
		Stderr:    stderr,
		Timestamp: time.Now(),
	})
	return m.injector.GenerateNegativeFeedback(command)
}

// GetFullFeedbackContext 获取完整历史失败上下文
// 应注入到 LLM system prompt 最前端
func (m *Manager) GetFullFeedbackContext() string {
	return m.injector.GenerateFullContext()
}

// Registry 暴露快照注册表（供外部查询）
func (m *Manager) Registry() *SnapshotRegistry {
	return m.registry
}

// Injector 暴露负反馈生成器
func (m *Manager) Injector() *FeedbackInjector {
	return m.injector
}

// sanitizeNodeID replaces characters that are invalid in Docker image references
// with hyphens, ensuring the resulting slug is safe to use as an image tag component.
func sanitizeNodeID(nodeID string) string {
	slug := nonAlphanumRe.ReplaceAllString(strings.ToLower(nodeID), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "node"
	}
	return slug
}
