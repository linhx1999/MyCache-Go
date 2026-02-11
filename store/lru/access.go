package lru

import (
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// Get 获取缓存项，如果存在且未过期则返回
func (c *LRU) Get(key string) (common.Value, bool) {
	c.rwMutex.RLock()
	elem, ok := c.entries[key]
	if !ok {
		c.rwMutex.RUnlock()
		return nil, false
	}

	// 检查是否过期
	if expTime, hasExp := c.expirationMap[key]; hasExp && time.Now().After(expTime) {
		c.rwMutex.RUnlock()

		// 异步删除过期项，避免在读锁内操作
		go c.Delete(key)

		return nil, false
	}

	// 获取值并释放读锁
	entry := elem.Value.(*cacheEntry)
	value := entry.value
	c.rwMutex.RUnlock()

	// 更新 LRU 位置需要写锁
	c.rwMutex.Lock()
	// 再次检查元素是否仍然存在（可能在获取写锁期间被其他协程删除）
	if _, ok := c.entries[key]; ok {
		c.lruList.MoveToBack(elem)
	}
	c.rwMutex.Unlock()

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

	c.rwMutex.Lock()
	defer c.rwMutex.Unlock()

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
