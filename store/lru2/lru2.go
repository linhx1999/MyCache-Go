package lru2

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// Cache 是 LRU2 两级缓存实现
type Cache struct {
	locks       []sync.Mutex
	caches      [][2]*cache
	onEvicted   func(key string, value common.Value)
	cleanupTick *time.Ticker
	mask        int32
}

// New 创建一个新的 LRU2 缓存实例
func New(bucketCount, capPerBucket, level2Cap uint16, cleanupInterval time.Duration, onEvicted func(string, common.Value)) *Cache {
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
	c := &Cache{
		locks:       make([]sync.Mutex, mask+1),
		caches:      make([][2]*cache, mask+1),
		onEvicted:   onEvicted,
		cleanupTick: time.NewTicker(cleanupInterval),
		mask:        int32(mask),
	}

	for i := range c.caches {
		c.caches[i][0] = createCache(capPerBucket)
		c.caches[i][1] = createCache(level2Cap)
	}

	if cleanupInterval > 0 {
		go c.cleanupLoop()
	}

	return c
}

// Get 获取缓存项
func (c *Cache) Get(key string) (common.Value, bool) {
	idx := hashBKRD(key) & c.mask
	c.locks[idx].Lock()
	defer c.locks[idx].Unlock()

	currentTime := now()

	// 首先检查一级缓存
	n1, status1, deadline := c.caches[idx][0].del(key)
	if status1 > 0 {
		// 从一级缓存找到项目
		if deadline > 0 && currentTime >= deadline {
			// 项目已过期，删除它
			c.delete(key, idx)
			fmt.Println("找到项目已过期，删除它")
			return nil, false
		}

		// 项目有效，将其移至二级缓存
		c.caches[idx][1].put(key, n1.value, deadline, c.onEvicted)
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
func (c *Cache) Set(key string, value common.Value) error {
	return c.SetWithExpiration(key, value, 9999999999999999*time.Nanosecond)
}

// SetWithExpiration 添加或更新缓存项，并设置过期时间
func (c *Cache) SetWithExpiration(key string, value common.Value, expiration time.Duration) error {
	// 计算过期时间 - 确保单位一致
	deadline := int64(0)
	if expiration > 0 {
		// now() 返回纳秒时间戳，确保 expiration 也是纳秒单位
		deadline = now() + int64(expiration.Nanoseconds())
	}

	idx := hashBKRD(key) & c.mask
	c.locks[idx].Lock()
	defer c.locks[idx].Unlock()

	// 放入一级缓存
	c.caches[idx][0].put(key, value, deadline, c.onEvicted)

	return nil
}

// Delete 从缓存中删除指定键的项
func (c *Cache) Delete(key string) bool {
	idx := hashBKRD(key) & c.mask
	c.locks[idx].Lock()
	defer c.locks[idx].Unlock()

	return c.delete(key, idx)
}

// Clear 清空缓存
func (c *Cache) Clear() {
	var keys []string

	for i := range c.caches {
		c.locks[i].Lock()

		c.caches[i][0].walk(func(key string, value common.Value, deadline int64) bool {
			keys = append(keys, key)
			return true
		})
		c.caches[i][1].walk(func(key string, value common.Value, deadline int64) bool {
			// 检查键是否已经收集（避免重复）
			for _, k := range keys {
				if key == k {
					return true
				}
			}
			keys = append(keys, key)
			return true
		})

		c.locks[i].Unlock()
	}

	for _, key := range keys {
		c.Delete(key)
	}
}

// Len 返回缓存中的项数
func (c *Cache) Len() int {
	count := 0

	for i := range c.caches {
		c.locks[i].Lock()

		c.caches[i][0].walk(func(key string, value common.Value, deadline int64) bool {
			count++
			return true
		})
		c.caches[i][1].walk(func(key string, value common.Value, deadline int64) bool {
			count++
			return true
		})

		c.locks[i].Unlock()
	}

	return count
}

// Close 关闭缓存，停止清理协程
func (c *Cache) Close() {
	if c.cleanupTick != nil {
		c.cleanupTick.Stop()
	}
}

// _get 内部方法，从指定级别的缓存获取项
func (c *Cache) _get(key string, idx, level int32) *cacheEntry {
	n := c.caches[idx][level].get(key)
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
func (c *Cache) delete(key string, idx int32) bool {
	n1, s1, _ := c.caches[idx][0].del(key)
	n2, s2, _ := c.caches[idx][1].del(key)
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
func (c *Cache) cleanupLoop() {
	for range c.cleanupTick.C {
		currentTime := now()

		for i := range c.caches {
			c.locks[i].Lock()

			// 检查并清理过期项目
			var expiredKeys []string

			c.caches[i][0].walk(func(key string, value common.Value, deadline int64) bool {
				if deadline > 0 && currentTime >= deadline {
					expiredKeys = append(expiredKeys, key)
				}
				return true
			})

			c.caches[i][1].walk(func(key string, value common.Value, deadline int64) bool {
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

			c.locks[i].Unlock()
		}
	}
}

// ============ 内部类型和方法 ============

// cacheEntry 表示 LRU2 缓存中的一个条目
type cacheEntry struct {
	key      string
	value    common.Value
	deadline int64 // 过期时间戳，deadline = 0 表示已删除
}

// cache 内部缓存核心实现，包含双向链表和节点存储
type cache struct {
	// dlnk[0]是哨兵节点，记录链表头尾，dlnk[0][p]存储尾部索引，dlnk[0][n]存储头部索引
	dlnk [][2]uint16            // 双向链表，0 表示前驱，1 表示后继
	m    []cacheEntry             // 预分配内存存储节点
	hmap map[string]uint16       // 键到节点索引的映射
	last uint16                  // 最后一个节点元素的索引
}

func createCache(cap uint16) *cache {
	return &cache{
		dlnk: make([][2]uint16, cap+1),
		m:    make([]cacheEntry, cap),
		hmap: make(map[string]uint16, cap),
		last: 0,
	}
}

// put 向缓存中添加项，如果是新增返回 1，更新返回 0
func (c *cache) put(key string, val common.Value, deadline int64, onEvicted func(string, common.Value)) int {
	if idx, ok := c.hmap[key]; ok {
		c.m[idx-1].value, c.m[idx-1].deadline = val, deadline
		c.adjust(idx, p, n) // 刷新到链表头部
		return 0
	}

	if c.last == uint16(cap(c.m)) {
		tail := &c.m[c.dlnk[0][p]-1]
		// 调用淘汰回调函数
		if onEvicted != nil && (*tail).deadline > 0 {
			onEvicted((*tail).key, (*tail).value)
		}

		delete(c.hmap, (*tail).key)
		c.hmap[key], (*tail).key, (*tail).value, (*tail).deadline = c.dlnk[0][p], key, val, deadline
		c.adjust(c.dlnk[0][p], p, n)

		return 1
	}

	c.last++
	if len(c.hmap) <= 0 {
		c.dlnk[0][p] = c.last
	} else {
		c.dlnk[c.dlnk[0][n]][p] = c.last
	}

	// 初始化新节点并更新链表指针
	c.m[c.last-1].key = key
	c.m[c.last-1].value = val
	c.m[c.last-1].deadline = deadline
	c.dlnk[c.last] = [2]uint16{0, c.dlnk[0][n]}
	c.hmap[key] = c.last
	c.dlnk[0][n] = c.last

	return 1
}

// get 从缓存中获取键对应的节点和状态
func (c *cache) get(key string) *cacheEntry {
	if idx, ok := c.hmap[key]; ok {
		c.adjust(idx, p, n)
		return &c.m[idx-1]
	}
	return nil
}

// del 从缓存中删除键对应的项
func (c *cache) del(key string) (*cacheEntry, int, int64) {
	if idx, ok := c.hmap[key]; ok && c.m[idx-1].deadline > 0 {
		d := c.m[idx-1].deadline
		c.m[idx-1].deadline = 0 // 标记为已删除
		c.adjust(idx, n, p)     // 移动到链表尾部
		return &c.m[idx-1], 1, d
	}

	return nil, 0, 0
}

// walk 遍历缓存中的所有有效项
func (c *cache) walk(walker func(key string, value common.Value, deadline int64) bool) {
	for idx := c.dlnk[0][n]; idx != 0; idx = c.dlnk[idx][n] {
		if c.m[idx-1].deadline > 0 && !walker(c.m[idx-1].key, c.m[idx-1].value, c.m[idx-1].deadline) {
			return
		}
	}
}

// adjust 调整节点在链表中的位置
// 当 f=0, t=1 时，移动到链表头部；否则移动到链表尾部
func (c *cache) adjust(idx, f, t uint16) {
	if c.dlnk[idx][f] != 0 {
		c.dlnk[c.dlnk[idx][t]][f] = c.dlnk[idx][f]
		c.dlnk[c.dlnk[idx][f]][t] = c.dlnk[idx][t]
		c.dlnk[idx][f] = 0
		c.dlnk[idx][t] = c.dlnk[0][t]
		c.dlnk[c.dlnk[0][t]][f] = idx
		c.dlnk[0][t] = idx
	}
}

// ============ 工具函数 ============

// 内部时钟，减少 time.Now() 调用造成的 GC 压力
var clock, p, n = time.Now().UnixNano(), uint16(0), uint16(1)

// now 返回 clock 变量的当前值
func now() int64 { return atomic.LoadInt64(&clock) }

func init() {
	go func() {
		for {
			atomic.StoreInt64(&clock, time.Now().UnixNano()) // 每秒校准一次
			for i := 0; i < 9; i++ {
				time.Sleep(100 * time.Millisecond)
				atomic.AddInt64(&clock, int64(100*time.Millisecond)) // 保持 clock 在一个精确的时间范围内，同时避免频繁的系统调用
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// hashBKRD 实现了 BKDR 哈希算法，用于计算键的哈希值
func hashBKRD(s string) (hash int32) {
	for i := 0; i < len(s); i++ {
		hash = hash*131 + int32(s[i])
	}

	return hash
}

// maskOfNextPowOf2 计算大于或等于输入值的最近 2 的幂次方减一作为掩码值
func maskOfNextPowOf2(cap uint16) uint16 {
	if cap > 0 && cap&(cap-1) == 0 {
		return cap - 1
	}

	// 通过多次右移和按位或操作，将二进制中最高的 1 位右边的所有位都填充为 1
	cap |= cap >> 1
	cap |= cap >> 2
	cap |= cap >> 4

	return cap | (cap >> 8)
}
