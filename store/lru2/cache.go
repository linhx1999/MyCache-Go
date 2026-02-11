package lru2

import (
	"sync"
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// LRU2Cache 是两级缓存实现（一级热点缓存 + 二级温数据缓存）
type LRU2Cache struct {
	bucketLocks   []sync.Mutex                         // 每个桶对应的锁，用于减少并发冲突
	buckets       [][2]*cacheBucket                    // 缓存桶数组，每个桶包含两级缓存：[0]一级热点缓存，[1]二级温数据缓存
	onEvicted     func(key string, value common.Value) // 缓存项被淘汰时的回调函数
	cleanupTicker *time.Ticker                         // 过期清理定时器，定期触发过期缓存扫描
	bucketMask    int32                                // 桶索引掩码，用于通过位运算快速定位桶（hash & bucketMask）
}

// keyToBucketIndex 计算 key 所在的桶索引
func (l *LRU2Cache) keyToBucketIndex(key string) int32 {
	return hashBKRD(key) & l.bucketMask
}

// Get 获取缓存项
func (l *LRU2Cache) Get(key string) (common.Value, bool) {
	// 计算 key 所在的桶索引：BKDR哈希 & 桶掩码，实现快速定位
	idx := l.keyToBucketIndex(key)

	// 获取该桶的锁，保证并发安全；使用细粒度锁减少锁竞争
	l.bucketLocks[idx].Lock()
	defer l.bucketLocks[idx].Unlock()

	// 获取当前时间戳（纳秒），用于判断是否过期
	currentTime := now()

	// ===== 步骤1：首先检查一级缓存（热点数据） =====
	// 使用 del 从一级缓存"删除"该 key（如果存在），以便后续移动到二级缓存
	// entry: 缓存条目指针, found: 是否找到, deadline: 过期时间点（-1表示永不过期）
	entry, found, deadline := l.buckets[idx][0].del(key)
	if found {
		// 在一级缓存中找到该 key

		// 检查是否已过期：deadline > 0 表示设置了过期时间，且当前时间已超过 deadline
		if deadline > 0 && currentTime >= deadline {
			// 项目已过期，从两级缓存中彻底删除
			l.delete(key, idx)
			// fmt.Printf("[LRU2] 缓存项已过期，执行删除: key=%s\n", key)
			return nil, false
		}

		// 项目有效：按照 LRU2 策略，从一级缓存"降级"到二级缓存
		// 因为刚被访问过，它在二级缓存会成为最新数据（头部）
		l.buckets[idx][1].put(key, entry.value, deadline, l.onEvicted)
		// fmt.Printf("[LRU2] 缓存项从一级降级到二级: key=%s\n", key)
		return entry.value, true
	}

	// ===== 步骤2：一级缓存未命中，检查二级缓存（温数据） =====
	// 从二级缓存获取条目（包含过期检查）
	entry2 := l.getFromLevel(key, idx, 1)
	if entry2 != nil {
		// 在二级缓存中找到，同样需要检查过期时间
		if entry2.deadline > 0 && currentTime >= entry2.deadline {
			// 项目已过期，从两级缓存中彻底删除
			l.delete(key, idx)
			// fmt.Printf("[LRU2] 缓存项已过期，执行删除: key=%s\n", key)
			return nil, false
		}

		// 二级缓存中找到且未过期，直接返回（不需要移动，保持在二级缓存）
		return entry2.value, true
	}

	// ===== 步骤3：两级缓存都未命中 =====
	return nil, false
}

// Set 添加或更新缓存项（永不过期）
func (l *LRU2Cache) Set(key string, value common.Value) error {
	idx := l.keyToBucketIndex(key)
	l.bucketLocks[idx].Lock()
	defer l.bucketLocks[idx].Unlock()

	// 放入一级缓存，-1 表示永不过期
	l.buckets[idx][0].put(key, value, -1, l.onEvicted)

	return nil
}

// SetWithExpiration 添加或更新缓存项，并设置过期时间
func (l *LRU2Cache) SetWithExpiration(key string, value common.Value, expiration time.Duration) error {
	// 计算过期时间
	var deadline int64
	if expiration > 0 {
		// now() 返回纳秒时间戳，确保 expiration 也是纳秒单位
		deadline = now() + int64(expiration.Nanoseconds())
	} else {
		// 负数表示永不过期
		deadline = -1
	}

	idx := l.keyToBucketIndex(key)
	l.bucketLocks[idx].Lock()
	defer l.bucketLocks[idx].Unlock()

	// 放入一级缓存
	l.buckets[idx][0].put(key, value, deadline, l.onEvicted)

	return nil
}

// Delete 从缓存中删除指定键的项
func (l *LRU2Cache) Delete(key string) bool {
	idx := l.keyToBucketIndex(key)
	l.bucketLocks[idx].Lock()
	defer l.bucketLocks[idx].Unlock()

	return l.delete(key, idx)
}

// Clear 清空缓存
func (l *LRU2Cache) Clear() {
	var keys []string

	for i := range l.buckets {
		l.bucketLocks[i].Lock()

		l.buckets[i][0].walk(func(key string, value common.Value, deadline int64) bool {
			keys = append(keys, key)
			return true
		})
		l.buckets[i][1].walk(func(key string, value common.Value, deadline int64) bool {
			// 检查键是否已经收集（避免重复）
			for _, k := range keys {
				if key == k {
					return true
				}
			}
			keys = append(keys, key)
			return true
		})

		l.bucketLocks[i].Unlock()
	}

	for _, key := range keys {
		l.Delete(key)
	}
}

// Len 返回缓存中的项数
func (l *LRU2Cache) Len() int {
	count := 0

	for i := range l.buckets {
		l.bucketLocks[i].Lock()

		l.buckets[i][0].walk(func(key string, value common.Value, deadline int64) bool {
			count++
			return true
		})
		l.buckets[i][1].walk(func(key string, value common.Value, deadline int64) bool {
			count++
			return true
		})

		l.bucketLocks[i].Unlock()
	}

	return count
}

// Close 关闭缓存，停止清理协程
func (l *LRU2Cache) Close() {
	if l.cleanupTicker != nil {
		l.cleanupTicker.Stop()
	}
}

// getFromLevel 从指定级别的缓存获取条目（包含过期检查）
func (l *LRU2Cache) getFromLevel(key string, idx, level int32) *cacheEntry {
	n := l.buckets[idx][level].get(key)
	if n != nil {
		currentTime := now()
		// deadline == 0: 已删除
		// deadline == -1: 永不过期
		// deadline > 0: 检查是否过期
		if n.deadline == 0 || (n.deadline > 0 && currentTime >= n.deadline) {
			return nil
		}
		return n
	}

	return nil
}

// delete 内部删除方法
func (l *LRU2Cache) delete(key string, idx int32) bool {
	n1, found1, _ := l.buckets[idx][0].del(key)
	n2, found2, _ := l.buckets[idx][1].del(key)
	deleted := found1 || found2

	// 调用淘汰回调函数
	if deleted {
		if n1 != nil && n1.value != nil && l.onEvicted != nil {
			l.onEvicted(key, n1.value)
		} else if n2 != nil && n2.value != nil && l.onEvicted != nil {
			l.onEvicted(key, n2.value)
		}
	}

	return deleted
}

// cleanupLoop 定期清理过期缓存的协程
func (l *LRU2Cache) cleanupLoop() {
	for range l.cleanupTicker.C {
		currentTime := now()

		for i := range l.buckets {
			l.bucketLocks[i].Lock()

			// 检查并清理过期项目
			var expiredKeys []string

			l.buckets[i][0].walk(func(key string, value common.Value, deadline int64) bool {
				if deadline > 0 && currentTime >= deadline {
					expiredKeys = append(expiredKeys, key)
				}
				return true
			})

			l.buckets[i][1].walk(func(key string, value common.Value, deadline int64) bool {
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
				l.delete(key, int32(i))
			}

			l.bucketLocks[i].Unlock()
		}
	}
}
