package sandbox

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Engine 管理 Docker 容器的生命周期
type Engine struct {
	cli         *client.Client
	containerID string
	config      Config
}

// NewEngine 创建 Docker Client 并拉起容器
func NewEngine(ctx context.Context, cfg Config) (*Engine, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	engine, err := createContainer(ctx, cli, cfg)
	if err != nil {
		cli.Close()
		return nil, err
	}
	engine.config = cfg
	return engine, nil
}

// newEngineWithClient 使用已有的 Docker Client 创建 Engine（内部使用，用于 RestartFrom）
func newEngineWithClient(ctx context.Context, cli *client.Client, cfg Config) (*Engine, error) {
	return createContainer(ctx, cli, cfg)
}

func createContainer(ctx context.Context, cli *client.Client, cfg Config) (*Engine, error) {
	// 容器配置：开启 TTY + Stdin，保持伪终端长连接
	containerCfg := &container.Config{
		Image:      cfg.Image,
		Tty:        true,
		OpenStdin:  true,
		WorkingDir: cfg.WorkingDir,
		Cmd:        []string{"/bin/bash"},
	}

	// 注入初始环境变量
	for k, v := range cfg.EnvVars {
		containerCfg.Env = append(containerCfg.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// 宿主机配置：运行时、资源限制
	hostCfg := &container.HostConfig{
		Resources: container.Resources{
			Memory:   cfg.MemoryLimit,
			CPUQuota: cfg.CPUQuota,
		},
	}

	// 指定运行时（gVisor runsc 或默认 runc）
	if cfg.Runtime != "" {
		hostCfg.Runtime = cfg.Runtime
	}

	// GPU 透传
	if cfg.GPUEnabled {
		hostCfg.DeviceRequests = []container.DeviceRequest{
			{
				Capabilities: [][]string{{"gpu"}},
				Count:        -1, // 所有可用 GPU
			},
		}
	}

	resp, err := cli.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// 启动失败则清理已创建的容器
		cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	return &Engine{
		cli:         cli,
		containerID: resp.ID,
		config:      cfg,
	}, nil
}

// ContainerID 返回当前容器 ID（暴露给 checkpoint 使用）
func (e *Engine) ContainerID() string {
	return e.containerID
}

// Client 返回底层 Docker Client（暴露给 checkpoint 使用）
func (e *Engine) Client() *client.Client {
	return e.cli
}

// Config 返回当前配置
func (e *Engine) Config() Config {
	return e.config
}

// removeContainer 仅移除容器，不关闭 Docker 客户端（供 RestartFrom 内部使用）
func (e *Engine) removeContainer(ctx context.Context) error {
	if e.containerID == "" {
		return nil
	}
	err := e.cli.ContainerRemove(ctx, e.containerID, container.RemoveOptions{Force: true})
	if err != nil {
		return fmt.Errorf("failed to remove container %s: %w", e.containerID, err)
	}
	e.containerID = ""
	return nil
}

// Destroy 强制销毁容器并关闭 Docker 客户端，释放所有资源
func (e *Engine) Destroy(ctx context.Context) error {
	if err := e.removeContainer(ctx); err != nil {
		return err
	}
	if e.cli != nil {
		return e.cli.Close()
	}
	return nil
}

// RestartFrom 基于指定镜像重建容器（checkpoint 回滚时调用）
// 销毁当前容器 → 用新镜像创建新容器 → 更新 containerID 和 config
func (e *Engine) RestartFrom(ctx context.Context, imageID string) (string, error) {
	// 仅移除容器，保留 Docker 客户端供后续使用
	if err := e.removeContainer(ctx); err != nil {
		return "", fmt.Errorf("failed to destroy old container during restart: %w", err)
	}

	// 用快照镜像重建，复用原有配置
	newCfg := e.config
	newCfg.Image = imageID

	newEngine, err := newEngineWithClient(ctx, e.cli, newCfg)
	if err != nil {
		return "", fmt.Errorf("failed to restart from image %s: %w", imageID, err)
	}

	e.containerID = newEngine.containerID
	e.config = newCfg // 同步更新 config，避免 Image 字段过期
	return e.containerID, nil
}
