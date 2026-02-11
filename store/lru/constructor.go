package lru

import (
	"container/list"
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// New 创建一个新的 LRU 缓存实例
func New(maxBytes int64, cleanupInterval time.Duration, onEvicted func(string, common.Value)) *LRU {
	// 设置默认清理间隔
	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}

	// 设置默认最大字节数
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024 // 8MB
	}

	c := &LRU{
		lruList:         list.New(),
		entries:         make(map[string]*list.Element),
		expirationMap:   make(map[string]time.Time),
		maxBytes:        maxBytes,
		onEvicted:       onEvicted,
		cleanupInterval: cleanupInterval,
		doneCh:          make(chan struct{}),
	}

	// 启动定期清理协程
	c.cleanupTicker = time.NewTicker(c.cleanupInterval)
	go c.cleanupLoop()

	return c
}
