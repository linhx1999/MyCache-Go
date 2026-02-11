package lru

import (
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// Get 获取缓存项，如果存在且未过期则返回
func (l *LRU) Get(key string) (common.Value, bool) {
	l.rwMutex.RLock()
	elem, ok := l.entries[key]
	if !ok {
		l.rwMutex.RUnlock()
		return nil, false
	}

	// 检查是否过期
	if expTime, hasExp := l.expirationMap[key]; hasExp && time.Now().After(expTime) {
		l.rwMutex.RUnlock()

		// 异步删除过期项，避免在读锁内操作
		go l.Delete(key)

		return nil, false
	}

	// 获取值并释放读锁
	entry := elem.Value.(*cacheEntry)
	value := entry.value
	l.rwMutex.RUnlock()

	// 更新 LRU 位置需要写锁
	l.rwMutex.Lock()
	// 再次检查元素是否仍然存在（可能在获取写锁期间被其他协程删除）
	if _, ok := l.entries[key]; ok {
		l.lruList.MoveToBack(elem)
	}
	l.rwMutex.Unlock()

	return value, true
}

// Set 添加或更新缓存项
func (l *LRU) Set(key string, value common.Value) error {
	return l.SetWithExpiration(key, value, 0)
}

// SetWithExpiration 添加或更新缓存项，并设置过期时间
func (l *LRU) SetWithExpiration(key string, value common.Value, expiration time.Duration) error {
	if value == nil {
		l.Delete(key)
		return nil
	}

	l.rwMutex.Lock()
	defer l.rwMutex.Unlock()

	// 计算过期时间
	var expTime time.Time
	if expiration > 0 {
		expTime = time.Now().Add(expiration)
		l.expirationMap[key] = expTime
	} else {
		delete(l.expirationMap, key)
	}

	// 如果键已存在，更新值
	if elem, ok := l.entries[key]; ok {
		entry := elem.Value.(*cacheEntry)
		l.usedBytes += int64(value.Len() - entry.value.Len())
		entry.value = value
		l.lruList.MoveToBack(elem)
		return nil
	}

	// 不存在，添加新项
	entry := &cacheEntry{key: key, value: value}
	elem := l.lruList.PushBack(entry)
	l.entries[key] = elem
	l.usedBytes += int64(len(key) + value.Len())

	// 检查是否需要淘汰旧项
	l.evict()

	return nil
}
