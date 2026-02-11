package lru2

import "github.com/linhx1999/MyCache-Go/store/common"

// cacheEntry 表示 LRU 缓存中的一个条目
type cacheEntry struct {
	key      string       // 缓存键
	value    common.Value // 缓存值
	deadline int64        // 过期时间戳（纳秒）：0 表示已删除，-1 表示永不过期，正数表示过期时间点
}

// cacheBucket 是单个 LRU 缓存桶的实现，包含双向链表和节点存储
type cacheBucket struct {
	// links[0]是哨兵节点，记录链表头尾，links[0][prev]存储尾部索引，links[0][next]存储头部索引
	links      [][2]uint16       // 双向链表数组，每个元素 [2]uint16 表示 [prev, next]，索引从 1 开始（0 为哨兵）
	entries    []cacheEntry      // 预分配的缓存条目数组，存储实际的键值对数据
	keyToIndex map[string]uint16 // 键到 entries 索引的映射（+1 后的值，0 表示不存在），用于 O(1) 查找
	size       uint16            // 当前已使用的条目数量，也是 entries 中的下一个可用位置
}

func createCache(cap uint16) *cacheBucket {
	return &cacheBucket{
		links:      make([][2]uint16, cap+1),
		entries:    make([]cacheEntry, cap),
		keyToIndex: make(map[string]uint16, cap),
		size:       0,
	}
}

// put 向缓存中添加项，如果是新增返回 1，更新返回 0
func (b *cacheBucket) put(key string, val common.Value, deadline int64, onEvicted func(string, common.Value)) int {
	if idx, ok := b.keyToIndex[key]; ok {
		b.entries[idx-1].value, b.entries[idx-1].deadline = val, deadline
		b.adjust(idx, head) // 刷新到链表头部
		return 0
	}

	if b.size == uint16(cap(b.entries)) {
		tail := &b.entries[b.links[0][prev]-1]
		// 调用淘汰回调函数
		if onEvicted != nil && (*tail).deadline > 0 {
			onEvicted((*tail).key, (*tail).value)
		}

		delete(b.keyToIndex, (*tail).key)
		b.keyToIndex[key], (*tail).key, (*tail).value, (*tail).deadline = b.links[0][prev], key, val, deadline
		b.adjust(b.links[0][prev], head)

		return 1
	}

	b.size++
	if len(b.keyToIndex) <= 0 {
		b.links[0][prev] = b.size
	} else {
		b.links[b.links[0][next]][prev] = b.size
	}

	// 初始化新节点并更新链表指针
	b.entries[b.size-1].key = key
	b.entries[b.size-1].value = val
	b.entries[b.size-1].deadline = deadline
	b.links[b.size] = [2]uint16{0, b.links[0][next]}
	b.keyToIndex[key] = b.size
	b.links[0][next] = b.size

	return 1
}

// get 从缓存中获取键对应的节点和状态
func (b *cacheBucket) get(key string) *cacheEntry {
	if idx, ok := b.keyToIndex[key]; ok {
		b.adjust(idx, head)
		return &b.entries[idx-1]
	}
	return nil
}

// del 从缓存中删除键对应的项
// 返回值：缓存条目、是否找到、过期时间
func (b *cacheBucket) del(key string) (*cacheEntry, bool, int64) {
	if idx, ok := b.keyToIndex[key]; ok && b.entries[idx-1].deadline != 0 {
		d := b.entries[idx-1].deadline
		b.entries[idx-1].deadline = 0 // 标记为已删除
		b.adjust(idx, tail)           // 移动到链表尾部
		return &b.entries[idx-1], true, d
	}

	return nil, false, 0
}

// walk 遍历缓存中的所有有效项（按访问顺序：从最近使用到最久未使用）
// walker 返回 false 表示停止遍历
func (b *cacheBucket) walk(walker func(key string, value common.Value, deadline int64) bool) {
	// 从链表头部（最近使用）开始遍历，直到回到哨兵节点（索引 0）
	for idx := b.links[0][next]; idx != 0; idx = b.links[idx][next] {
		entry := &b.entries[idx-1]

		// 跳过已标记删除的项（deadline == 0）
		if entry.deadline == 0 {
			continue
		}

		// 调用 walker，如果返回 false 则停止遍历
		if !walker(entry.key, entry.value, entry.deadline) {
			return
		}
	}
}

// adjust 将指定节点从当前位置移动到链表的目标端（头部或尾部）
//
// 参数说明：
//   - nodeIdx: 要移动的节点索引（在 links 数组中的位置，1-based）
//   - target:  目标位置（head 表示移动到头部，tail 表示移动到尾部）
//
// 使用场景：
//   - 移动到头部（刷新访问）：adjust(idx, head)
//   - 移动到尾部（准备淘汰）：adjust(idx, tail)
//
// 链表结构示意：
//   哨兵节点(0): [prev=尾索引, next=头索引]
//   普通节点(i): [prev=前驱索引, next=后继索引]
func (b *cacheBucket) adjust(nodeIdx, target uint16) {
	// 计算相反方向：如果 target 是 head(1)，则 opposite 是 tail(0)，反之亦然
	opposite := 1 - target

	// 如果节点已经在目标位置（相反方向为 0 表示已在头部/尾部），无需调整
	if b.links[nodeIdx][opposite] == 0 {
		return
	}

	// 获取当前节点的前后连接关系
	currPrev := b.links[nodeIdx][prev] // 当前节点的前驱
	currNext := b.links[nodeIdx][next] // 当前节点的后继

	// 步骤1：将当前节点从链表中"摘下"（跳过当前节点）
	// 前驱节点的 next 指向当前节点的后继
	b.links[currPrev][next] = currNext
	// 后继节点的 prev 指向当前节点的前驱
	b.links[currNext][prev] = currPrev

	// 步骤2：将当前节点插入到链表的目标端（头部或尾部）
	// 获取当前目标端的头部节点索引（哨兵节点的 target 方向）
	targetHead := b.links[0][target]

	// 当前节点的 opposite 方向指向 0（哨兵），表示它现在是端点
	b.links[nodeIdx][opposite] = 0
	// 当前节点的 target 方向指向原来的目标头部
	b.links[nodeIdx][target] = targetHead
	// 原来目标头部的 opposite 方向指向当前节点
	b.links[targetHead][opposite] = nodeIdx
	// 哨兵节点的 target 方向更新为当前节点（当前节点成为新的目标端点）
	b.links[0][target] = nodeIdx
}
