package lru2

import "github.com/linhx1999/MyCache-Go/store/common"

// cacheEntry 表示 LRU 缓存中的一个条目
type cacheEntry struct {
	key      string       // 缓存键
	value    common.Value // 缓存值
	deadline int64        // 过期时间戳（纳秒），0 表示已删除，正数表示过期时间点
}

// cache 是单个 LRU 缓存桶的实现，包含双向链表和节点存储
type cache struct {
	// links[0]是哨兵节点，记录链表头尾，links[0][prev]存储尾部索引，links[0][next]存储头部索引
	links      [][2]uint16       // 双向链表数组，每个元素 [2]uint16 表示 [prev, next]，索引从 1 开始（0 为哨兵）
	entries    []cacheEntry      // 预分配的缓存条目数组，存储实际的键值对数据
	keyToIndex map[string]uint16 // 键到 entries 索引的映射（+1 后的值，0 表示不存在），用于 O(1) 查找
	size       uint16            // 当前已使用的条目数量，也是 entries 中的下一个可用位置
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

// adjust 将指定节点从当前位置移动到链表的目标端（头部或尾部）
//
// 参数说明：
//   - idx: 要移动的节点索引（在 links 数组中的位置，1-based）
//   - from: 当前连接方向（0=prev 表示从后往前连接，1=next 表示从前往后连接）
//   - to:   目标连接方向（与 from 相反，0=prev 或 1=next）
//
// 使用场景：
//   - 移动到头部（刷新访问）：adjust(idx, prev, next)  // from=0(prev), to=1(next)
//   - 移动到尾部（准备淘汰）：adjust(idx, next, prev)  // from=1(next), to=0(prev)
//
// 链表结构示意：
//   哨兵节点(0): [prev=尾索引, next=头索引]
//   普通节点(i): [prev=前驱索引, next=后继索引]
func (c *cache) adjust(idx, from, to uint16) {
	// 如果节点已经在目标位置（from 方向为 0 表示已在头部/尾部），无需调整
	if c.links[idx][from] == 0 {
		return
	}

	// 获取当前节点的前后连接关系
	currPrev := c.links[idx][prev] // 当前节点的前驱
	currNext := c.links[idx][next] // 当前节点的后继

	// 步骤1：将当前节点从链表中"摘下"（跳过当前节点）
	// 前驱节点的 next 指向当前节点的后继
	c.links[currPrev][next] = currNext
	// 后继节点的 prev 指向当前节点的前驱
	c.links[currNext][prev] = currPrev

	// 步骤2：将当前节点插入到链表的目标端（头部或尾部）
	// 获取当前目标端的头部节点索引（哨兵节点的 to 方向）
	targetHead := c.links[0][to]

	// 当前节点的 from 方向指向 0（哨兵），表示它现在是端点
	c.links[idx][from] = 0
	// 当前节点的 to 方向指向原来的目标头部
	c.links[idx][to] = targetHead
	// 原来目标头部的 from 方向指向当前节点
	c.links[targetHead][from] = idx
	// 哨兵节点的 to 方向更新为当前节点（当前节点成为新的目标端点）
	c.links[0][to] = idx
}
