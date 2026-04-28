package checkpoint

import (
	"sync"
	"time"
)

// Snapshot 单个快照记录
type Snapshot struct {
	NodeID    string // DAG 节点 ID
	ImageID   string // Docker Commit 生成的镜像 ID
	CreatedAt int64  // Unix 时间戳
	Comment   string // 快照备注
}

// SnapshotRegistry 有序的快照注册表
// 记录 DAG 节点 ID 与 Docker imageID 的映射关系，支持链式回溯
type SnapshotRegistry struct {
	snapshots []Snapshot
	index     map[string]int // nodeID → snapshots 切片索引
	mu        sync.RWMutex
}

// NewSnapshotRegistry 创建快照注册表
func NewSnapshotRegistry() *SnapshotRegistry {
	return &SnapshotRegistry{
		snapshots: make([]Snapshot, 0),
		index:     make(map[string]int),
	}
}

// Register 注册新快照
func (r *SnapshotRegistry) Register(snap Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if snap.CreatedAt == 0 {
		snap.CreatedAt = time.Now().Unix()
	}

	// 如果同一个 nodeID 已存在，覆盖
	if idx, ok := r.index[snap.NodeID]; ok {
		r.snapshots[idx] = snap
		return
	}

	r.index[snap.NodeID] = len(r.snapshots)
	r.snapshots = append(r.snapshots, snap)
}

// GetByNodeID 通过 DAG 节点 ID 查找快照
func (r *SnapshotRegistry) GetByNodeID(nodeID string) (Snapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx, ok := r.index[nodeID]
	if !ok {
		return Snapshot{}, false
	}
	return r.snapshots[idx], true
}

// GetLatest 获取最近的成功快照
func (r *SnapshotRegistry) GetLatest() (Snapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.snapshots) == 0 {
		return Snapshot{}, false
	}
	return r.snapshots[len(r.snapshots)-1], true
}

// RollbackTo 回滚注册表到指定节点（删除该节点之后的所有快照记录）
func (r *SnapshotRegistry) RollbackTo(nodeID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx, ok := r.index[nodeID]
	if !ok {
		return false
	}

	// 删除 idx 之后的快照索引
	for i := idx + 1; i < len(r.snapshots); i++ {
		delete(r.index, r.snapshots[i].NodeID)
	}
	r.snapshots = r.snapshots[:idx+1]
	return true
}

// Len 返回当前快照数量
func (r *SnapshotRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.snapshots)
}

// All 返回所有快照的副本（只读）
func (r *SnapshotRegistry) All() []Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make([]Snapshot, len(r.snapshots))
	copy(cp, r.snapshots)
	return cp
}
