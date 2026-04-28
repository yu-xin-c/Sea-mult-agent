package checkpoint_test

import (
	"testing"

	"github.com/yu-xin-c/Sea-mult-agent/docker-core/checkpoint"
)

func TestSnapshotRegistry_RegisterAndGet(t *testing.T) {
	reg := checkpoint.NewSnapshotRegistry()

	reg.Register(checkpoint.Snapshot{NodeID: "node-A", ImageID: "img-aaa"})
	reg.Register(checkpoint.Snapshot{NodeID: "node-B", ImageID: "img-bbb"})

	if reg.Len() != 2 {
		t.Fatalf("expected 2 snapshots, got %d", reg.Len())
	}

	snap, ok := reg.GetByNodeID("node-A")
	if !ok {
		t.Fatal("node-A not found")
	}
	if snap.ImageID != "img-aaa" {
		t.Errorf("expected img-aaa, got %s", snap.ImageID)
	}

	_, ok = reg.GetByNodeID("node-C")
	if ok {
		t.Error("node-C should not exist")
	}
}

func TestSnapshotRegistry_GetLatest(t *testing.T) {
	reg := checkpoint.NewSnapshotRegistry()

	_, ok := reg.GetLatest()
	if ok {
		t.Error("should return false when empty")
	}

	reg.Register(checkpoint.Snapshot{NodeID: "node-A", ImageID: "img-aaa"})
	reg.Register(checkpoint.Snapshot{NodeID: "node-B", ImageID: "img-bbb"})

	snap, ok := reg.GetLatest()
	if !ok {
		t.Fatal("should return latest")
	}
	if snap.NodeID != "node-B" {
		t.Errorf("expected node-B, got %s", snap.NodeID)
	}
}

func TestSnapshotRegistry_RollbackTo(t *testing.T) {
	reg := checkpoint.NewSnapshotRegistry()

	reg.Register(checkpoint.Snapshot{NodeID: "node-A", ImageID: "img-aaa"})
	reg.Register(checkpoint.Snapshot{NodeID: "node-B", ImageID: "img-bbb"})
	reg.Register(checkpoint.Snapshot{NodeID: "node-C", ImageID: "img-ccc"})

	// 回滚到 node-A，应该删除 node-B 和 node-C
	ok := reg.RollbackTo("node-A")
	if !ok {
		t.Fatal("rollback should succeed")
	}

	if reg.Len() != 1 {
		t.Fatalf("expected 1 snapshot after rollback, got %d", reg.Len())
	}

	_, ok = reg.GetByNodeID("node-B")
	if ok {
		t.Error("node-B should be removed after rollback")
	}

	_, ok = reg.GetByNodeID("node-C")
	if ok {
		t.Error("node-C should be removed after rollback")
	}
}

func TestSnapshotRegistry_RollbackToNonExistent(t *testing.T) {
	reg := checkpoint.NewSnapshotRegistry()
	reg.Register(checkpoint.Snapshot{NodeID: "node-A", ImageID: "img-aaa"})

	ok := reg.RollbackTo("node-X")
	if ok {
		t.Error("rollback to non-existent node should fail")
	}
}

func TestSnapshotRegistry_OverwriteSameNodeID(t *testing.T) {
	reg := checkpoint.NewSnapshotRegistry()

	reg.Register(checkpoint.Snapshot{NodeID: "node-A", ImageID: "img-v1"})
	reg.Register(checkpoint.Snapshot{NodeID: "node-A", ImageID: "img-v2"})

	if reg.Len() != 1 {
		t.Fatalf("expected 1 snapshot (overwrite), got %d", reg.Len())
	}

	snap, _ := reg.GetByNodeID("node-A")
	if snap.ImageID != "img-v2" {
		t.Errorf("expected img-v2 after overwrite, got %s", snap.ImageID)
	}
}

func TestSnapshotRegistry_All(t *testing.T) {
	reg := checkpoint.NewSnapshotRegistry()
	reg.Register(checkpoint.Snapshot{NodeID: "a", ImageID: "1"})
	reg.Register(checkpoint.Snapshot{NodeID: "b", ImageID: "2"})

	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}

	// 修改副本不影响原始
	all[0].NodeID = "modified"
	snap, _ := reg.GetByNodeID("a")
	if snap.NodeID != "a" {
		t.Error("All() should return a copy")
	}
}
