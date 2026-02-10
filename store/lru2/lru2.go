package lru2

import (
	"fmt"
	"sync"
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// LRU2 是两级缓存实现（一级热点缓存 + 二级温数据缓存）
type LRU2 struct {
	bucketLocks   []sync.Mutex
	buckets       [][2]*cache
	onEvicted     func(key string, value common.Value)
	cleanupTicker *time.Ticker
	bucketMask    int32
}

// New 创建一个新的 LRU2 缓存实例
func New(bucketCount, capPerBucket, level2Cap uint16, cleanupInterval time.Duration, onEvicted func(string, common.Value)) *LRU2 {
	if bucketCount == 0 {
		bucketCount = 16
	}
	if capPerBucket == 0 {
		capPerBucket = 1024
	}
	if level2Cap == 0 {
		level2Cap = 1024
	}
	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}

	mask := maskOfNextPowOf2(bucketCount)
	c := &LRU2{
		bucketLocks:   make([]sync.Mutex, mask+1),
		buckets:       make([][2]*cache, mask+1),
		onEvicted:     onEvicted,
		cleanupTicker: time.NewTicker(cleanupInterval),
		bucketMask:    int32(mask),
	}

	for i := range c.buckets {
		c.buckets[i][0] = createCache(capPerBucket)
		c.buckets[i][1] = createCache(level2Cap)
	}

	if cleanupInterval > 0 {
		go c.cleanupLoop()
	}

	return c
}

// Get 获取缓存项
func (c *LRU2) Get(key string) (common.Value, bool) {
	idx := hashBKRD(key) & c.bucketMask
	c.bucketLocks[idx].Lock()
	defer c.bucketLocks[idx].Unlock()

	currentTime := now()

	// 首先检查一级缓存
	n1, status1, deadline := c.buckets[idx][0].del(key)
	if status1 > 0 {
		// 从一级缓存找到项目
		if deadline > 0 && currentTime >= deadline {
			// 项目已过期，删除它
			c.delete(key, idx)
			fmt.Println("找到项目已过期，删除它")
			return nil, false
		}

		// 项目有效，将其移至二级缓存
		c.buckets[idx][1].put(key, n1.value, deadline, c.onEvicted)
		fmt.Println("项目有效，将其移至二级缓存")
		return n1.value, true
	}

	// 一级缓存未找到，检查二级缓存
	n2 := c._get(key, idx, 1)
	if n2 != nil {
		if n2.deadline > 0 && currentTime >= n2.deadline {
			// 项目已过期，删除它
			c.delete(key, idx)
			fmt.Println("找到项目已过期，删除它")
			return nil, false
		}

		return n2.value, true
	}

	return nil, false
}

// Set 添加或更新缓存项
func (c *LRU2) Set(key string, value common.Value) error {
	return c.SetWithExpiration(key, value, 9999999999999999*time.Nanosecond)
}

// SetWithExpiration 添加或更新缓存项，并设置过期时间
func (c *LRU2) SetWithExpiration(key string, value common.Value, expiration time.Duration) error {
	// 计算过期时间 - 确保单位一致
	deadline := int64(0)
	if expiration > 0 {
		// now() 返回纳秒时间戳，确保 expiration 也是纳秒单位
		deadline = now() + int64(expiration.Nanoseconds())
	}

	idx := hashBKRD(key) & c.bucketMask
	c.bucketLocks[idx].Lock()
	defer c.bucketLocks[idx].Unlock()

	// 放入一级缓存
	c.buckets[idx][0].put(key, value, deadline, c.onEvicted)

	return nil
}

// Delete 从缓存中删除指定键的项
func (c *LRU2) Delete(key string) bool {
	idx := hashBKRD(key) & c.bucketMask
	c.bucketLocks[idx].Lock()
	defer c.bucketLocks[idx].Unlock()

	return c.delete(key, idx)
}

// Clear 清空缓存
func (c *LRU2) Clear() {
	var keys []string

	for i := range c.buckets {
		c.bucketLocks[i].Lock()

		c.buckets[i][0].walk(func(key string, value common.Value, deadline int64) bool {
			keys = append(keys, key)
			return true
		})
		c.buckets[i][1].walk(func(key string, value common.Value, deadline int64) bool {
			// 检查键是否已经收集（避免重复）
			for _, k := range keys {
				if key == k {
					return true
				}
			}
			keys = append(keys, key)
			return true
		})

		c.bucketLocks[i].Unlock()
	}

	for _, key := range keys {
		c.Delete(key)
	}
}

// Len 返回缓存中的项数
func (c *LRU2) Len() int {
	count := 0

	for i := range c.buckets {
		c.bucketLocks[i].Lock()

		c.buckets[i][0].walk(func(key string, value common.Value, deadline int64) bool {
			count++
			return true
		})
		c.buckets[i][1].walk(func(key string, value common.Value, deadline int64) bool {
			count++
			return true
		})

		c.bucketLocks[i].Unlock()
	}

	return count
}

// Close 关闭缓存，停止清理协程
func (c *LRU2) Close() {
	if c.cleanupTicker != nil {
		c.cleanupTicker.Stop()
	}
}

// _get 内部方法，从指定级别的缓存获取项
func (c *LRU2) _get(key string, idx, level int32) *cacheEntry {
	n := c.buckets[idx][level].get(key)
	if n != nil {
		currentTime := now()
		if n.deadline <= 0 || currentTime >= n.deadline {
			// 过期或已删除
			return nil
		}
		return n
	}

	return nil
}

// delete 内部删除方法
func (c *LRU2) delete(key string, idx int32) bool {
	n1, s1, _ := c.buckets[idx][0].del(key)
	n2, s2, _ := c.buckets[idx][1].del(key)
	deleted := s1 > 0 || s2 > 0

	// 调用淘汰回调函数
	if deleted {
		if n1 != nil && n1.value != nil && c.onEvicted != nil {
			c.onEvicted(key, n1.value)
		} else if n2 != nil && n2.value != nil && c.onEvicted != nil {
			c.onEvicted(key, n2.value)
		}
	}

	return deleted
}

// cleanupLoop 定期清理过期缓存的协程
func (c *LRU2) cleanupLoop() {
	for range c.cleanupTicker.C {
		currentTime := now()

		for i := range c.buckets {
			c.bucketLocks[i].Lock()

			// 检查并清理过期项目
			var expiredKeys []string

			c.buckets[i][0].walk(func(key string, value common.Value, deadline int64) bool {
				if deadline > 0 && currentTime >= deadline {
					expiredKeys = append(expiredKeys, key)
				}
				return true
			})

			c.buckets[i][1].walk(func(key string, value common.Value, deadline int64) bool {
				if deadline > 0 && currentTime >= deadline {
					for _, k := range expiredKeys {
						if key == k {
							// 避免重复
							return true
						}
					}
					expiredKeys = append(expiredKeys, key)
				}
				return true
			})

			for _, key := range expiredKeys {
				c.delete(key, int32(i))
			}

			c.bucketLocks[i].Unlock()
		}
	}
}
