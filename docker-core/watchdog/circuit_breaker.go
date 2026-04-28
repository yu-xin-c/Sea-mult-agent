package watchdog

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// CircuitBreaker 语义断路器
type CircuitBreaker struct {
	mu           sync.Mutex
	windowSize   int
	recentHashes []string
}

func NewCircuitBreaker(windowSize int) *CircuitBreaker {
	if windowSize <= 0 {
		windowSize = 5
	}
	return &CircuitBreaker{
		windowSize:   windowSize,
		recentHashes: make([]string, 0, windowSize),
	}
}

// RecordAndCheck 记录指令及其结果的哈希，并检查是否触发熔断
func (cb *CircuitBreaker) RecordAndCheck(cmd string, output string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// 语义特征：指令内容 + 输出摘要（简单处理）
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(cmd+output)))

	// 更新滑动窗口
	cb.recentHashes = append(cb.recentHashes, hash)
	if len(cb.recentHashes) > cb.windowSize {
		cb.recentHashes = cb.recentHashes[1:]
	}

	// 检查窗口内是否存在过多相同的哈希
	count := 0
	for _, h := range cb.recentHashes {
		if h == hash {
			count++
		}
	}

	// 如果窗口内相同的哈希超过阈值（如占窗口一半以上，或达到特定次数）
	// 这里简单定义为：如果窗口填满了且全是同一个哈希
	return count >= cb.windowSize && len(cb.recentHashes) == cb.windowSize
}

func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.recentHashes = cb.recentHashes[:0]
}
