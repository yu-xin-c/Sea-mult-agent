package executor

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// Gatekeeper 资源网关，管理 GPU 和 IO 并发
type Gatekeeper struct {
	ioSem  *semaphore.Weighted
	gpuSem *semaphore.Weighted
}

func NewGatekeeper(cfg ResourceConfig) *Gatekeeper {
	return &Gatekeeper{
		ioSem:  semaphore.NewWeighted(cfg.MaxConcurrentIO),
		gpuSem: semaphore.NewWeighted(cfg.MaxGPUTasks),
	}
}

func (gk *Gatekeeper) AcquireIO(ctx context.Context, n int64) error {
	return gk.ioSem.Acquire(ctx, n)
}

func (gk *Gatekeeper) ReleaseIO(n int64) {
	gk.ioSem.Release(n)
}

func (gk *Gatekeeper) AcquireGPU(ctx context.Context, n int64) error {
	return gk.gpuSem.Acquire(ctx, n)
}

func (gk *Gatekeeper) ReleaseGPU(n int64) {
	gk.gpuSem.Release(n)
}
