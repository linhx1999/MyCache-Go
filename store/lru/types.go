package lru

import (
	"container/list"
	"sync"
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// LRU 是基于标准库 list 的 LRU 缓存实现
type LRU struct {
	mu              sync.RWMutex
	lruList         *list.List                           // 双向链表，用于维护 LRU 顺序
	entries         map[string]*list.Element             // 键到链表节点的映射
	expirationMap   map[string]time.Time                 // 过期时间映射
	maxBytes        int64                                // 最大允许字节数
	usedBytes       int64                                // 当前使用的字节数
	onEvicted       func(key string, value common.Value) // 淘汰回调函数，当缓存项被淘汰时调用
	cleanupInterval time.Duration                        // 定期清理过期缓存的时间间隔
	cleanupTicker   *time.Ticker                         // 定时器，用于触发定期清理任务
	doneCh          chan struct{}                        // 用于优雅关闭清理协程
}

// cacheEntry 表示缓存中的一个条目
type cacheEntry struct {
	key   string
	value common.Value
}
