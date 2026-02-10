package lru2

import "github.com/linhx1999/MyCache-Go/store/common"

// cacheEntry 表示 LRU 缓存中的一个条目
type cacheEntry struct {
	key      string
	value    common.Value
	deadline int64 // 过期时间戳，deadline = 0 表示已删除
}

// cache 是单个 LRU 缓存桶的实现，包含双向链表和节点存储
type cache struct {
	// links[0]是哨兵节点，记录链表头尾，links[0][prev]存储尾部索引，links[0][next]存储头部索引
	links      [][2]uint16       // 双向链表，0 表示前驱(prev)，1 表示后继(next)
	entries    []cacheEntry      // 预分配内存存储节点
	keyToIndex map[string]uint16 // 键到节点索引的映射
	size       uint16            // 当前已使用的条目数量
}

func createCache(cap uint16) *cache {
	return &cache{
		links:      make([][2]uint16, cap+1),
		entries:    make([]cacheEntry, cap),
		keyToIndex: make(map[string]uint16, cap),
		size:       0,
	}
}

// put 向缓存中添加项，如果是新增返回 1，更新返回 0
func (c *cache) put(key string, val common.Value, deadline int64, onEvicted func(string, common.Value)) int {
	if idx, ok := c.keyToIndex[key]; ok {
		c.entries[idx-1].value, c.entries[idx-1].deadline = val, deadline
		c.adjust(idx, prev, next) // 刷新到链表头部
		return 0
	}

	if c.size == uint16(cap(c.entries)) {
		tail := &c.entries[c.links[0][prev]-1]
		// 调用淘汰回调函数
		if onEvicted != nil && (*tail).deadline > 0 {
			onEvicted((*tail).key, (*tail).value)
		}

		delete(c.keyToIndex, (*tail).key)
		c.keyToIndex[key], (*tail).key, (*tail).value, (*tail).deadline = c.links[0][prev], key, val, deadline
		c.adjust(c.links[0][prev], prev, next)

		return 1
	}

	c.size++
	if len(c.keyToIndex) <= 0 {
		c.links[0][prev] = c.size
	} else {
		c.links[c.links[0][next]][prev] = c.size
	}

	// 初始化新节点并更新链表指针
	c.entries[c.size-1].key = key
	c.entries[c.size-1].value = val
	c.entries[c.size-1].deadline = deadline
	c.links[c.size] = [2]uint16{0, c.links[0][next]}
	c.keyToIndex[key] = c.size
	c.links[0][next] = c.size

	return 1
}

// get 从缓存中获取键对应的节点和状态
func (c *cache) get(key string) *cacheEntry {
	if idx, ok := c.keyToIndex[key]; ok {
		c.adjust(idx, prev, next)
		return &c.entries[idx-1]
	}
	return nil
}

// del 从缓存中删除键对应的项
func (c *cache) del(key string) (*cacheEntry, int, int64) {
	if idx, ok := c.keyToIndex[key]; ok && c.entries[idx-1].deadline > 0 {
		d := c.entries[idx-1].deadline
		c.entries[idx-1].deadline = 0 // 标记为已删除
		c.adjust(idx, next, prev)     // 移动到链表尾部
		return &c.entries[idx-1], 1, d
	}

	return nil, 0, 0
}

// walk 遍历缓存中的所有有效项
func (c *cache) walk(walker func(key string, value common.Value, deadline int64) bool) {
	for idx := c.links[0][next]; idx != 0; idx = c.links[idx][next] {
		if c.entries[idx-1].deadline > 0 && !walker(c.entries[idx-1].key, c.entries[idx-1].value, c.entries[idx-1].deadline) {
			return
		}
	}
}

// adjust 调整节点在链表中的位置
// 当 from=prev(0), to=next(1) 时，移动到链表头部；否则移动到链表尾部
func (c *cache) adjust(idx, from, to uint16) {
	if c.links[idx][from] != 0 {
		c.links[c.links[idx][to]][from] = c.links[idx][from]
		c.links[c.links[idx][from]][to] = c.links[idx][to]
		c.links[idx][from] = 0
		c.links[idx][to] = c.links[0][to]
		c.links[c.links[0][to]][from] = idx
		c.links[0][to] = idx
	}
}
