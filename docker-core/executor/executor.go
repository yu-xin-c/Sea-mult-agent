package executor

import (
	"context"
)

// Sandbox 接口定义，方便 Engine 调用
type Sandbox interface {
	Execute(ctx context.Context, cmd string) (string, error)
	Close() error
}

// CheckpointManager 接口定义
type CheckpointManager interface {
	Commit(ctx context.Context, label string) (string, error)
	Rollback(ctx context.Context, id string) error
}

// Agent 接口定义，方便进行 Mock 测试
type Agent interface {
	Plan(ctx context.Context, goal string) (*DAG, error)
	GenerateStrategies(ctx context.Context, instruction string) ([]string, error)
}

// ResourceConfig 资源配置
type ResourceConfig struct {
	MaxConcurrentIO int64
	MaxGPUTasks     int64
}

// Node DAG 节点
type Node struct {
	ID           string
	Commands     []string
	Dependencies []*Node
	InDegree     int // 用于调度
}

// DAG 有向无环图
type DAG struct {
	Nodes map[string]*Node
}

func NewDAG() *DAG {
	return &DAG{Nodes: make(map[string]*Node)}
}

func (d *DAG) AddNode(id string, cmds []string) *Node {
	node := &Node{ID: id, Commands: cmds}
	d.Nodes[id] = node
	return node
}

func (d *DAG) AddEdge(from, to *Node) {
	to.Dependencies = append(to.Dependencies, from)
	to.InDegree++
}
