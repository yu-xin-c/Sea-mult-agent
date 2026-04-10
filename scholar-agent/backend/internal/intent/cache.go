package intent

import (
	"fmt"
	"sync"
	"time"
)

// cacheItem 缓存项，包含值和过期时间
type cacheItem struct {
	value     interface{}
	expireAt  time.Time
}

// MemoryCache 带 TTL 的内存缓存（替代 Redis）
type MemoryCache struct {
	items sync.Map
	stop  chan struct{}
}

// NewMemoryCache 创建内存缓存实例，并启动后台清理 goroutine
func NewMemoryCache() *MemoryCache {
	c := &MemoryCache{
		stop: make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

// Set 设置缓存值，ttl 为过期时间
func (c *MemoryCache) Set(key string, value interface{}, ttl time.Duration) {
	c.items.Store(key, &cacheItem{
		value:    value,
		expireAt: time.Now().Add(ttl),
	})
}

// Get 获取缓存值，若不存在或已过期返回 nil, false
func (c *MemoryCache) Get(key string) (interface{}, bool) {
	raw, ok := c.items.Load(key)
	if !ok {
		return nil, false
	}
	item := raw.(*cacheItem)
	if time.Now().After(item.expireAt) {
		c.items.Delete(key)
		return nil, false
	}
	return item.value, true
}

// GetString 获取字符串类型的缓存值
func (c *MemoryCache) GetString(key string) (string, bool) {
	val, ok := c.Get(key)
	if !ok {
		return "", false
	}
	s, ok := val.(string)
	return s, ok
}

// Delete 删除缓存项
func (c *MemoryCache) Delete(key string) {
	c.items.Delete(key)
}

// Close 停止后台清理
func (c *MemoryCache) Close() {
	close(c.stop)
}

// cleanupLoop 后台定时清理过期键
func (c *MemoryCache) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.items.Range(func(key, value interface{}) bool {
				item := value.(*cacheItem)
				if time.Now().After(item.expireAt) {
					c.items.Delete(key)
				}
				return true
			})
		case <-c.stop:
			return
		}
	}
}

// 缓存 Key 模板常量
const (
	// CacheKeyLastHistory 上一轮对话记录 - 格式: last_history_{userId}_{deviceId}
	CacheKeyLastHistory = "last_history_%d_%s"
	// CacheKeyLastHistoryTTL 上一轮对话记录过期时间
	CacheKeyLastHistoryTTL = 5 * time.Minute
)

// LastHistoryKey 生成上一轮对话缓存 Key
func LastHistoryKey(userId int64, deviceId string) string {
	return fmt.Sprintf(CacheKeyLastHistory, userId, deviceId)
}
