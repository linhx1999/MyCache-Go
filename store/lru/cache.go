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
	lruList         *list.List                      // 双向链表，用于维护 LRU 顺序
	entries         map[string]*list.Element        // 键到链表节点的映射
	expirationMap   map[string]time.Time            // 过期时间映射
	maxBytes        int64                           // 最大允许字节数
	usedBytes       int64                           // 当前使用的字节数
	onEvicted       func(key string, value common.Value) // 淘汰回调函数，当缓存项被淘汰时调用
	cleanupInterval time.Duration                   // 定期清理过期缓存的时间间隔
	cleanupTicker   *time.Ticker                    // 定时器，用于触发定期清理任务
	doneCh          chan struct{}                   // 用于优雅关闭清理协程
}

// cacheEntry 表示缓存中的一个条目
type cacheEntry struct {
	key   string
	value common.Value
}

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

// Get 获取缓存项，如果存在且未过期则返回
func (c *LRU) Get(key string) (common.Value, bool) {
	c.mu.RLock()
	elem, ok := c.entries[key]
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}

	// 检查是否过期
	if expTime, hasExp := c.expirationMap[key]; hasExp && time.Now().After(expTime) {
		c.mu.RUnlock()

		// 异步删除过期项，避免在读锁内操作
		go c.Delete(key)

		return nil, false
	}

	// 获取值并释放读锁
	entry := elem.Value.(*cacheEntry)
	value := entry.value
	c.mu.RUnlock()

	// 更新 LRU 位置需要写锁
	c.mu.Lock()
	// 再次检查元素是否仍然存在（可能在获取写锁期间被其他协程删除）
	if _, ok := c.entries[key]; ok {
		c.lruList.MoveToBack(elem)
	}
	c.mu.Unlock()

	return value, true
}

// Set 添加或更新缓存项
func (c *LRU) Set(key string, value common.Value) error {
	return c.SetWithExpiration(key, value, 0)
}

// SetWithExpiration 添加或更新缓存项，并设置过期时间
func (c *LRU) SetWithExpiration(key string, value common.Value, expiration time.Duration) error {
	if value == nil {
		c.Delete(key)
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 计算过期时间
	var expTime time.Time
	if expiration > 0 {
		expTime = time.Now().Add(expiration)
		c.expirationMap[key] = expTime
	} else {
		delete(c.expirationMap, key)
	}

	// 如果键已存在，更新值
	if elem, ok := c.entries[key]; ok {
		oldEntry := elem.Value.(*cacheEntry)
		c.usedBytes += int64(value.Len() - oldEntry.value.Len())
		oldEntry.value = value
		c.lruList.MoveToBack(elem)
		return nil
	}

	// 添加新项
	entry := &cacheEntry{key: key, value: value}
	elem := c.lruList.PushBack(entry)
	c.entries[key] = elem
	c.usedBytes += int64(len(key) + value.Len())

	// 检查是否需要淘汰旧项
	c.evict()

	return nil
}

// Delete 从缓存中删除指定键的项
func (c *LRU) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[key]; ok {
		c.removeElement(elem)
		return true
	}
	return false
}

// Clear 清空缓存
func (c *LRU) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 遍历所有项调用回调函数
	for _, elem := range c.entries {
		entry := elem.Value.(*cacheEntry)
		if c.onEvicted != nil {
			c.onEvicted(entry.key, entry.value)
		}
	}

	c.lruList.Init()
	c.entries = make(map[string]*list.Element)
	c.expirationMap = make(map[string]time.Time)
	c.usedBytes = 0
}

// Len 返回缓存中的项数
func (c *LRU) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lruList.Len()
}

// Close 关闭缓存，停止清理协程
func (c *LRU) Close() {
	if c.cleanupTicker != nil {
		c.cleanupTicker.Stop()
		close(c.doneCh)
	}
}

// removeElement 从缓存中删除元素，调用此方法前必须持有锁
func (c *LRU) removeElement(elem *list.Element) {
	entry := elem.Value.(*cacheEntry)
	c.lruList.Remove(elem)
	delete(c.entries, entry.key)
	delete(c.expirationMap, entry.key)
	c.usedBytes -= int64(len(entry.key) + entry.value.Len())

	// 调用淘汰回调函数
	if c.onEvicted != nil {
		c.onEvicted(entry.key, entry.value)
	}
}

// evict 清理过期和超出内存限制的缓存，调用此方法前必须持有锁
func (c *LRU) evict() {
	// 先清理过期项
	now := time.Now()
	for key, expTime := range c.expirationMap {
		if now.After(expTime) {
			if elem, ok := c.entries[key]; ok {
				c.removeElement(elem)
			}
		}
	}

	// 再根据内存限制清理最久未使用的项
	for c.maxBytes > 0 && c.usedBytes > c.maxBytes && c.lruList.Len() > 0 {
		elem := c.lruList.Front() // 获取最久未使用的项（链表头部）
		if elem != nil {
			c.removeElement(elem)
		}
	}
}

// cleanupLoop 定期清理过期缓存的协程
func (c *LRU) cleanupLoop() {
	for {
		select {
		case <-c.cleanupTicker.C:
			c.mu.Lock()
			c.evict()
			c.mu.Unlock()
		case <-c.doneCh:
			return
		}
	}
}
