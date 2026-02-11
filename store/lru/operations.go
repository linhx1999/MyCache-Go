package lru

import (
	"container/list"
	"time"
)

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
