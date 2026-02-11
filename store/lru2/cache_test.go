package lru2

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// ============================================================================
// 测试辅助类型和函数
// ============================================================================

// testValue 为测试定义一个简单的 Value 类型
type testValue string

func (v testValue) Len() int {
	return len(v)
}

// testValueBytes 用于测试字节值
type testValueBytes []byte

func (v testValueBytes) Len() int {
	return len(v)
}

// ============================================================================
// cacheBucket 单元测试
// ============================================================================

// TestCacheBucket_BasicOperations 测试缓存桶的基本操作
func TestCacheBucket_BasicOperations(t *testing.T) {
	t.Run("初始化缓存桶", func(t *testing.T) {
		bucket := createCache(10)
		if bucket == nil {
			t.Fatal("创建缓存桶失败")
		}
		if bucket.size != 0 {
			t.Fatalf("初始 size 应为 0，实际为 %d", bucket.size)
		}
		if len(bucket.entries) != 10 {
			t.Fatalf("缓存桶容量应为 10，实际为 %d", len(bucket.entries))
		}
		if len(bucket.links) != 11 {
			t.Fatalf("链表长度应为 cap+1(11)，实际为 %d", len(bucket.links))
		}
		if len(bucket.keyToIndex) != 0 {
			t.Fatalf("初始 keyToIndex 应为空，实际为 %d", len(bucket.keyToIndex))
		}
	})

	t.Run("添加和获取", func(t *testing.T) {
		bucket := createCache(5)
		var evictCount int
		onEvicted := func(key string, value common.Value) {
			evictCount++
		}

		// 添加新项，返回 1 表示新增
		status := bucket.put("key1", testValue("value1"), 100, onEvicted)
		if status != 1 {
			t.Fatalf("添加新项应返回 1，实际返回 %d", status)
		}
		if bucket.size != 1 {
			t.Fatalf("添加一项后 size 应为 1，实际为 %d", bucket.size)
		}

		// 获取项
		entry := bucket.get("key1")
		if entry == nil {
			t.Fatal("获取项返回了 nil")
		}
		if entry.key != "key1" || entry.value.(testValue) != "value1" || entry.deadline != 100 {
			t.Fatalf("获取项值不一致: %+v", *entry)
		}

		// 获取不存在的项
		entry = bucket.get("不存在")
		if entry != nil {
			t.Fatal("获取不存在项应返回 nil")
		}

		// 更新现有项，返回 0 表示更新
		status = bucket.put("key1", testValue("新值"), 200, onEvicted)
		if status != 0 {
			t.Fatalf("更新项应返回 0，实际返回 %d", status)
		}
		if bucket.size != 1 {
			t.Fatalf("更新后 size 仍应为 1，实际为 %d", bucket.size)
		}

		// 验证更新后的值
		entry = bucket.get("key1")
		if entry.value.(testValue) != "新值" || entry.deadline != 200 {
			t.Fatalf("更新项后值不一致: %+v", *entry)
		}

		if evictCount != 0 {
			t.Fatalf("不应有淘汰，实际淘汰 %d 次", evictCount)
		}
	})

	t.Run("删除操作", func(t *testing.T) {
		bucket := createCache(5)

		// 添加项
		bucket.put("key1", testValue("value1"), 100, nil)

		// 删除存在的项
		entry, found, deadline := bucket.del("key1")
		if !found {
			t.Fatal("删除存在项应返回 true")
		}
		if entry == nil {
			t.Fatal("删除应返回被删除的条目")
		}
		if entry.deadline != 0 {
			t.Fatalf("删除后条目 deadline 应为 0，实际为 %d", entry.deadline)
		}
		if deadline != 100 {
			t.Fatalf("删除应返回原始 deadline(100)，实际为 %d", deadline)
		}

		// 注意：bucket.get 不检查 deadline，所以仍能通过 get 获取
		// 实际删除检查在 LRU2Cache 层面处理
		entry = bucket.get("key1")
		// get 只是移动节点位置并返回条目，不检查 deadline
		if entry == nil {
			t.Fatal("bucket.get 在底层不检查 deadline，应返回条目")
		}
		// 但条目的 deadline 应为 0（已删除标记）
		if entry.deadline != 0 {
			t.Fatalf("已删除条目的 deadline 应为 0，实际为 %d", entry.deadline)
		}

		// 验证 walk 不会遍历已删除的项（walk 会检查 deadline）
		var keys []string
		bucket.walk(func(key string, value common.Value, deadline int64) bool {
			keys = append(keys, key)
			return true
		})
		if contains(keys, "key1") {
			t.Fatal("walk 不应遍历已删除的项")
		}

		// 删除不存在的项
		_, found, _ = bucket.del("不存在")
		if found {
			t.Fatal("删除不存在项应返回 false")
		}
	})

	t.Run("容量和淘汰", func(t *testing.T) {
		bucket := createCache(3) // 容量为 3 的缓存
		var evictedKeys []string

		onEvicted := func(key string, value common.Value) {
			evictedKeys = append(evictedKeys, key)
		}

		// 填满缓存
		for i := 1; i <= 3; i++ {
			bucket.put(fmt.Sprintf("key%d", i), testValue(fmt.Sprintf("value%d", i)), 100, onEvicted)
		}

		// 再添加一项，应该淘汰最早的 key1
		bucket.put("key4", testValue("value4"), 100, onEvicted)

		if len(evictedKeys) != 1 {
			t.Fatalf("应淘汰 1 项，实际淘汰 %d 项", len(evictedKeys))
		}
		// 注意：由于 LRU 机制，最早添加的是 key1，但它被访问后会移动到头部
		// 所以需要根据实际 LRU 行为来判断

		// 验证缓存状态
		if bucket.get("key1") != nil {
			t.Fatal("key1 应已被淘汰")
		}
	})
}

// TestCacheBucket_LRUEviction 测试 LRU 淘汰策略
func TestCacheBucket_LRUEviction(t *testing.T) {
	var evictedKeys []string
	onEvicted := func(key string, value common.Value) {
		evictedKeys = append(evictedKeys, key)
	}

	bucket := createCache(3)

	// 添加 3 个项
	bucket.put("key1", testValue("value1"), now()+int64(time.Hour), onEvicted)
	bucket.put("key2", testValue("value2"), now()+int64(time.Hour), onEvicted)
	bucket.put("key3", testValue("value3"), now()+int64(time.Hour), onEvicted)

	if len(evictedKeys) != 0 {
		t.Errorf("Expected no evictions, got %v", evictedKeys)
	}

	// 访问 key1 使其成为最近使用的
	bucket.get("key1")

	// 添加第 4 个项，应该淘汰最少使用的 key2
	bucket.put("key4", testValue("value4"), now()+int64(time.Hour), onEvicted)

	if len(evictedKeys) != 1 {
		t.Errorf("Expected 1 eviction, got %d: %v", len(evictedKeys), evictedKeys)
	}

	// 验证 key2 已被淘汰（无法获取）
	if bucket.get("key2") != nil {
		t.Errorf("Expected key2 to be evicted")
	}

	// 验证其他键仍然存在
	keys := []string{"key1", "key3", "key4"}
	for _, key := range keys {
		if bucket.get(key) == nil {
			t.Errorf("Expected %s to exist", key)
		}
	}
}

// TestCacheBucket_Walk 测试遍历方法
func TestCacheBucket_Walk(t *testing.T) {
	bucket := createCache(5)

	// 添加几个项
	bucket.put("key1", testValue("value1"), now()+int64(time.Hour), nil)
	bucket.put("key2", testValue("value2"), now()+int64(time.Hour), nil)
	bucket.put("key3", testValue("value3"), now()+int64(time.Hour), nil)

	// 删除一个项
	bucket.del("key2")

	// 使用 walk 收集所有项
	var keys []string
	bucket.walk(func(key string, value common.Value, deadline int64) bool {
		keys = append(keys, key)
		return true
	})

	// 验证只有未删除的项被遍历
	if len(keys) != 2 {
		t.Errorf("Walk should return 2 keys, got %d: %v", len(keys), keys)
	}

	// 测试提前终止遍历
	count := 0
	bucket.walk(func(key string, value common.Value, deadline int64) bool {
		count++
		return false // 只处理第一个项
	})

	if count != 1 {
		t.Errorf("Walk didn't stop early as expected, count=%d", count)
	}
}

// TestCacheBucket_Adjust 测试 adjust 方法
func TestCacheBucket_Adjust(t *testing.T) {
	bucket := createCache(5)

	// 添加几个项以形成链表
	bucket.put("key1", testValue("value1"), now()+int64(time.Hour), nil)
	bucket.put("key2", testValue("value2"), now()+int64(time.Hour), nil)
	bucket.put("key3", testValue("value3"), now()+int64(time.Hour), nil)

	// 获取 key1 的索引
	idx1 := bucket.keyToIndex["key1"]

	// 将 key1 移动到链表头部
	bucket.adjust(idx1, head)

	// 验证 key1 现在是最近使用的（链表头）
	if bucket.links[0][next] != idx1 {
		t.Errorf("Expected key1 to be at the head of the list, got %d", bucket.links[0][next])
	}

	// 将 key1 移动到链表尾部
	bucket.adjust(idx1, tail)

	// 验证 key1 现在是最少使用的（链表尾）
	if bucket.links[0][prev] != idx1 {
		t.Errorf("Expected key1 to be at the tail of the list, got %d", bucket.links[0][prev])
	}
}

// TestCacheBucket_EdgeCases 测试边界条件
func TestCacheBucket_EdgeCases(t *testing.T) {
	t.Run("空键", func(t *testing.T) {
		bucket := createCache(5)
		bucket.put("", testValue("empty-key-value"), 100, nil)

		entry := bucket.get("")
		if entry == nil || entry.value.(testValue) != "empty-key-value" {
			t.Error("应能存储和获取空键")
		}
	})

	t.Run("零值", func(t *testing.T) {
		bucket := createCache(5)
		bucket.put("zero", testValue(""), 100, nil)

		entry := bucket.get("zero")
		if entry == nil || entry.value.(testValue) != "" {
			t.Error("应能存储和获取空值")
		}
	})

	t.Run("永不过期", func(t *testing.T) {
		bucket := createCache(5)
		bucket.put("never", testValue("value"), -1, nil)

		entry := bucket.get("never")
		if entry == nil || entry.deadline != -1 {
			t.Error("应能设置永不过期的项")
		}
	})

	t.Run("容量为 1", func(t *testing.T) {
		bucket := createCache(1)
		var evicted []string
		onEvicted := func(key string, value common.Value) {
			evicted = append(evicted, key)
		}

		bucket.put("key1", testValue("value1"), 100, onEvicted)
		bucket.put("key2", testValue("value2"), 100, onEvicted)

		if len(evicted) != 1 || evicted[0] != "key1" {
			t.Errorf("应淘汰 key1，实际淘汰: %v", evicted)
		}

		if bucket.get("key1") != nil {
			t.Error("key1 应已被淘汰")
		}
		if bucket.get("key2") == nil {
			t.Error("key2 应存在")
		}
	})

	t.Run("重复更新同一键", func(t *testing.T) {
		bucket := createCache(5)

		for i := 0; i < 100; i++ {
			bucket.put("key", testValue(fmt.Sprintf("value%d", i)), int64(i), nil)
		}

		entry := bucket.get("key")
		if entry == nil || entry.value.(testValue) != "value99" {
			t.Error("应保留最后一次更新的值")
		}

		if bucket.size != 1 {
			t.Errorf("重复更新同一键不应增加 size，实际 size=%d", bucket.size)
		}
	})
}

// ============================================================================
// LRU2Cache 单元测试
// ============================================================================

// TestLRU2Cache_BasicOperations 测试 LRU2Cache 基本操作
func TestLRU2Cache_BasicOperations(t *testing.T) {
	t.Run("Set 和 Get", func(t *testing.T) {
		var evictedKeys []string
		onEvicted := func(key string, value common.Value) {
			evictedKeys = append(evictedKeys, fmt.Sprintf("%s:%v", key, value))
		}

		cache := New(4, 2, 3, time.Minute, onEvicted)
		defer cache.Close()

		// 测试 Set 和 Get
		err := cache.Set("key1", testValue("value1"))
		if err != nil {
			t.Errorf("Set failed: %v", err)
		}

		value, found := cache.Get("key1")
		if !found || value != testValue("value1") {
			t.Errorf("Get failed, expected 'value1', got %v, found: %v", value, found)
		}

		// 测试更新
		err = cache.Set("key1", testValue("value1-updated"))
		if err != nil {
			t.Errorf("Set update failed: %v", err)
		}

		value, found = cache.Get("key1")
		if !found || value != testValue("value1-updated") {
			t.Errorf("Get after update failed, expected 'value1-updated', got %v", value)
		}

		// 测试不存在的键
		value, found = cache.Get("nonexistent")
		if found {
			t.Errorf("Get nonexistent key should return false, got %v, %v", value, found)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		cache := New(4, 2, 3, time.Minute, nil)
		defer cache.Close()

		cache.Set("key1", testValue("value1"))

		// 测试删除
		deleted := cache.Delete("key1")
		if !deleted {
			t.Errorf("Delete should return true")
		}

		_, found := cache.Get("key1")
		if found {
			t.Errorf("Get after delete should return false")
		}

		// 测试删除不存在的键
		deleted = cache.Delete("nonexistent")
		if deleted {
			t.Errorf("Delete nonexistent key should return false")
		}
	})

	t.Run("Len", func(t *testing.T) {
		cache := New(4, 10, 10, time.Minute, nil)
		defer cache.Close()

		if cache.Len() != 0 {
			t.Errorf("初始长度应为 0，实际为 %d", cache.Len())
		}

		for i := 0; i < 5; i++ {
			cache.Set(fmt.Sprintf("key%d", i), testValue(fmt.Sprintf("value%d", i)))
		}

		if cache.Len() != 5 {
			t.Errorf("添加 5 项后长度应为 5，实际为 %d", cache.Len())
		}

		cache.Delete("key0")
		if cache.Len() != 4 {
			t.Errorf("删除 1 项后长度应为 4，实际为 %d", cache.Len())
		}
	})
}

// TestLRU2Cache_TwoLevelCache 测试两级缓存机制
// 注意：Set 操作导致的淘汰会直接丢弃数据，只有 Get 操作会触发从 L1 降级到 L2
func TestLRU2Cache_TwoLevelCache(t *testing.T) {
	// 单桶以简化测试，L1 容量 2，L2 容量 1（L2 容量为 1 便于测试淘汰）
	cache := New(1, 2, 1, time.Minute, nil)
	defer cache.Close()

	// 步骤 1: 填满 L1
	cache.Set("key1", testValue("value1"))
	cache.Set("key2", testValue("value2"))

	// 步骤 2: 获取 key1，将其从 L1 降级到 L2
	// Get 操作会：从 L1 删除 key1 -> 放入 L2
	value, found := cache.Get("key1")
	if !found || value != testValue("value1") {
		t.Errorf("key1 should be found and moved to L2")
	}

	// 步骤 3: 此时 L1 只有 key2，可以再添加一个 key3
	// L1 = [key3, key2], L2 = [key1]
	cache.Set("key3", testValue("value3"))

	// 步骤 4: 访问 L1 中的 key2，将其降级到 L2
	// 这会导致 L2 溢出（容量为 1，已有 key1），key1 被淘汰
	value, found = cache.Get("key2")
	if !found || value != testValue("value2") {
		t.Errorf("key2 should be found in L1 and moved to L2")
	}

	// 步骤 5: 验证 key1 已被淘汰（key2 将其挤出 L2）
	_, found = cache.Get("key1")
	if found {
		t.Errorf("key1 should be evicted from level2")
	}

	// 步骤 6: 验证 key2 现在在 L2 中
	value, found = cache.Get("key2")
	if !found || value != testValue("value2") {
		t.Errorf("key2 should exist")
	}

	// 步骤 7: 验证 L1 中的 key3 仍在
	value, found = cache.Get("key3")
	if !found || value != testValue("value3") {
		t.Errorf("key3 should still exist in L1")
	}
}

// TestLRU2Cache_LevelPromotion 测试缓存级别降级（Get 操作将数据从 L1 移到 L2）
func TestLRU2Cache_LevelPromotion(t *testing.T) {
	// 使用单桶以便测试，L1 容量 2，L2 容量 3
	cache := New(1, 2, 3, time.Minute, nil)
	defer cache.Close()

	// 填满一级缓存
	cache.Set("key1", testValue("value1"))
	cache.Set("key2", testValue("value2"))

	// 获取 key1，将其从 L1 降级到 L2
	value, found := cache.Get("key1")
	if !found || value != testValue("value1") {
		t.Errorf("key1 should be found")
	}

	// 此时：L1 = [key2], L2 = [key1]
	// 再次填满一级缓存
	cache.Set("key3", testValue("value3"))
	// 此时：L1 = [key3, key2], L2 = [key1]

	// 获取 key2，将其从 L1 降级到 L2
	value, found = cache.Get("key2")
	if !found || value != testValue("value2") {
		t.Errorf("key2 should be found")
	}

	// 此时：L1 = [key3], L2 = [key2, key1]

	// key1 应该仍在二级缓存中
	value, found = cache.Get("key1")
	if !found || value != testValue("value1") {
		t.Errorf("key1 should still exist in level2")
	}
}

// TestLRU2Cache_Expiration 测试过期时间
func TestLRU2Cache_Expiration(t *testing.T) {
	t.Run("基本过期", func(t *testing.T) {
		cache := New(1, 5, 5, 100*time.Millisecond, nil)
		defer cache.Close()

		// 添加一个很快过期的项
		shortDuration := 200 * time.Millisecond
		cache.SetWithExpiration("expires-soon", testValue("value"), shortDuration)

		// 添加一个不会很快过期的项
		cache.SetWithExpiration("expires-later", testValue("value"), time.Hour)

		// 验证都能获取到
		_, found := cache.Get("expires-soon")
		if !found {
			t.Errorf("expires-soon should be found initially")
		}

		_, found = cache.Get("expires-later")
		if !found {
			t.Errorf("expires-later should be found")
		}

		// 等待短期项过期
		time.Sleep(300 * time.Millisecond)

		// 验证短期项已过期，长期项仍存在
		_, found = cache.Get("expires-soon")
		if found {
			t.Errorf("expires-soon should have expired")
		}

		_, found = cache.Get("expires-later")
		if !found {
			t.Errorf("expires-later should still be valid")
		}
	})

	t.Run("永不过期", func(t *testing.T) {
		cache := New(1, 5, 5, time.Minute, nil)
		defer cache.Close()

		// 使用 Set（永不过期）
		cache.Set("never-expire", testValue("value"))

		// 使用 SetWithExpiration 设置 0 或负数也应该是永不过期
		cache.SetWithExpiration("zero-duration", testValue("value"), 0)
		cache.SetWithExpiration("negative-duration", testValue("value"), -1*time.Second)

		// 所有项都应该存在
		for _, key := range []string{"never-expire", "zero-duration", "negative-duration"} {
			_, found := cache.Get(key)
			if !found {
				t.Errorf("%s should exist", key)
			}
		}
	})

	t.Run("过期后重新设置", func(t *testing.T) {
		cache := New(1, 5, 5, time.Minute, nil)
		defer cache.Close()

		// 设置一个短期过期的项
		cache.SetWithExpiration("key", testValue("value1"), 50*time.Millisecond)
		time.Sleep(100 * time.Millisecond)

		// 已过期
		_, found := cache.Get("key")
		if found {
			t.Error("项应该已过期")
		}

		// 重新设置
		cache.Set("key", testValue("value2"))

		value, found := cache.Get("key")
		if !found || value != testValue("value2") {
			t.Error("重新设置后应能获取新值")
		}
	})
}

// TestLRU2Cache_CleanupLoop 测试清理循环
func TestLRU2Cache_CleanupLoop(t *testing.T) {
	cache := New(1, 5, 5, 100*time.Millisecond, nil)
	defer cache.Close()

	// 添加几个很快过期的项
	shortDuration := 200 * time.Millisecond
	cache.SetWithExpiration("expires1", testValue("value1"), shortDuration)
	cache.SetWithExpiration("expires2", testValue("value2"), shortDuration)

	// 添加一个不会很快过期的项
	cache.SetWithExpiration("keeps", testValue("value"), time.Hour)

	// 等待项过期并被清理循环处理
	time.Sleep(500 * time.Millisecond)

	// 验证过期项已被清理
	_, found := cache.Get("expires1")
	if found {
		t.Errorf("expires1 should have been cleaned up")
	}

	_, found = cache.Get("expires2")
	if found {
		t.Errorf("expires2 should have been cleaned up")
	}

	// 验证未过期项仍然存在
	_, found = cache.Get("keeps")
	if !found {
		t.Errorf("keeps should still be valid")
	}
}

// TestLRU2Cache_Clear 测试清空缓存
func TestLRU2Cache_Clear(t *testing.T) {
	var evictedKeys []string
	onEvicted := func(key string, value common.Value) {
		evictedKeys = append(evictedKeys, key)
	}

	cache := New(2, 5, 5, time.Minute, onEvicted)
	defer cache.Close()

	// 添加一些项
	for i := 0; i < 10; i++ {
		cache.Set(fmt.Sprintf("key%d", i), testValue(fmt.Sprintf("value%d", i)))
	}

	// 验证长度
	if length := cache.Len(); length != 10 {
		t.Errorf("Expected length 10, got %d", length)
	}

	// 清空缓存
	cache.Clear()

	// 验证长度为 0
	if length := cache.Len(); length != 0 {
		t.Errorf("Expected length 0 after Clear, got %d", length)
	}

	// 验证项已被删除
	for i := 0; i < 10; i++ {
		_, found := cache.Get(fmt.Sprintf("key%d", i))
		if found {
			t.Errorf("key%d should not be found after Clear", i)
		}
	}

	// 验证淘汰回调被调用
	if len(evictedKeys) != 10 {
		t.Errorf("Expected 10 evicted keys, got %d", len(evictedKeys))
	}
}

// TestLRU2Cache_Concurrent 测试并发操作
func TestLRU2Cache_Concurrent(t *testing.T) {
	cache := New(8, 100, 200, time.Minute, nil)
	defer cache.Close()

	const goroutines = 10
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()

			// 每个协程操作自己的一组键
			prefix := fmt.Sprintf("g%d-", id)

			// 添加操作
			for i := 0; i < operationsPerGoroutine; i++ {
				key := prefix + strconv.Itoa(i)
				value := testValue(fmt.Sprintf("value-%s", key))

				err := cache.Set(key, value)
				if err != nil {
					t.Errorf("Set failed: %v", err)
				}
			}

			// 获取操作
			for i := 0; i < operationsPerGoroutine; i++ {
				key := prefix + strconv.Itoa(i)
				expectedValue := testValue(fmt.Sprintf("value-%s", key))

				value, found := cache.Get(key)
				if !found {
					t.Errorf("Get failed for key %s", key)
				} else if value != expectedValue {
					t.Errorf("Get returned wrong value for %s: expected %s, got %v", key, expectedValue, value)
				}
			}

			// 删除操作
			for i := 0; i < operationsPerGoroutine/2; i++ {
				key := prefix + strconv.Itoa(i)
				cache.Delete(key)
			}
		}(g)
	}

	wg.Wait()

	// 验证大致长度
	expectedItems := goroutines * operationsPerGoroutine / 2
	actualItems := cache.Len()

	tolerance := expectedItems / 10
	if actualItems < expectedItems-tolerance || actualItems > expectedItems+tolerance {
		t.Errorf("Expected approximately %d items, got %d", expectedItems, actualItems)
	}
}

// TestLRU2Cache_ConcurrentSameKey 测试并发访问同一键
func TestLRU2Cache_ConcurrentSameKey(t *testing.T) {
	cache := New(4, 10, 10, time.Minute, nil)
	defer cache.Close()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// 并发读写同一键
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()

			key := "shared-key"
			value := testValue(fmt.Sprintf("value-%d", id))

			// 设置
			cache.Set(key, value)

			// 获取
			cache.Get(key)

			// 部分协程删除
			if id%10 == 0 {
				cache.Delete(key)
			}
		}(i)
	}

	wg.Wait()

	// 只要没有 panic 就算通过
	t.Log("Concurrent same key test passed")
}

// TestLRU2Cache_ConcurrentExpiration 测试并发过期场景
func TestLRU2Cache_ConcurrentExpiration(t *testing.T) {
	cache := New(4, 50, 50, 50*time.Millisecond, nil)
	defer cache.Close()

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// 并发设置不同过期时间的项
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()

			for i := 0; i < 20; i++ {
				key := fmt.Sprintf("g%d-key%d", id, i)
				duration := time.Duration((i%3)+1) * 100 * time.Millisecond
				cache.SetWithExpiration(key, testValue("value"), duration)
			}
		}(g)
	}

	wg.Wait()

	// 等待部分项过期
	time.Sleep(400 * time.Millisecond)

	// 验证部分项已过期
	expiredCount := 0
	validCount := 0
	for g := 0; g < goroutines; g++ {
		for i := 0; i < 20; i++ {
			key := fmt.Sprintf("g%d-key%d", g, i)
			_, found := cache.Get(key)
			if found {
				validCount++
			} else {
				expiredCount++
			}
		}
	}

	t.Logf("Valid: %d, Expired: %d", validCount, expiredCount)

	// 应该有一些过期，一些仍然有效
	if expiredCount == 0 {
		t.Error("应该有一些项已过期")
	}
}

// ============================================================================
// 边界条件测试
// ============================================================================

// TestLRU2Cache_EdgeCases 测试边界条件
func TestLRU2Cache_EdgeCases(t *testing.T) {
	t.Run("空字符串键", func(t *testing.T) {
		cache := New(4, 10, 10, time.Minute, nil)
		defer cache.Close()

		cache.Set("", testValue("empty"))
		value, found := cache.Get("")
		if !found || value != testValue("empty") {
			t.Error("应能处理空字符串键")
		}
	})

	t.Run("特殊字符键", func(t *testing.T) {
		cache := New(4, 10, 10, time.Minute, nil)
		defer cache.Close()

		specialKeys := []string{
			"key with spaces",
			"key\twith\ttabs",
			"key\nwith\nnewlines",
			"key/with/slashes",
			"key.with.dots",
			"🔥emoji🔥",
			"中文键",
		}

		for _, key := range specialKeys {
			cache.Set(key, testValue("value"))
			value, found := cache.Get(key)
			if !found || value != testValue("value") {
				t.Errorf("应能处理特殊键: %q", key)
			}
		}
	})

	t.Run("大数据值", func(t *testing.T) {
		cache := New(4, 10, 10, time.Minute, nil)
		defer cache.Close()

		// 1MB 数据
		bigValue := testValueBytes(make([]byte, 1024*1024))
		cache.Set("big", bigValue)

		value, found := cache.Get("big")
		if !found {
			t.Error("应能存储大数据值")
		}
		if value.Len() != 1024*1024 {
			t.Errorf("大数据值长度不匹配: got %d", value.Len())
		}
	})

	t.Run("极小容量", func(t *testing.T) {
		// 每个桶容量为 1
		cache := New(1, 1, 1, time.Minute, nil)
		defer cache.Close()

		cache.Set("key1", testValue("value1"))
		cache.Set("key2", testValue("value2"))

		// key1 可能被淘汰或在二级缓存中
		_, found := cache.Get("key1")
		// 不检查具体结果，只确保不 panic
		t.Logf("key1 found: %v", found)

		_, found = cache.Get("key2")
		if !found {
			t.Error("key2 应该存在")
		}
	})

	t.Run("大量键", func(t *testing.T) {
		cache := New(16, 100, 100, time.Minute, nil)
		defer cache.Close()

		// 添加大量键
		for i := 0; i < 1000; i++ {
			key := fmt.Sprintf("key-%d", i)
			cache.Set(key, testValue(fmt.Sprintf("value-%d", i)))
		}

		// 验证部分键存在
		hitCount := 0
		for i := 0; i < 1000; i++ {
			key := fmt.Sprintf("key-%d", i)
			if _, found := cache.Get(key); found {
				hitCount++
			}
		}

		t.Logf("Hit count: %d/1000", hitCount)
		// 由于 LRU 淘汰，不是所有键都存在
		if hitCount == 0 {
			t.Error("应该有部分键存在")
		}
	})
}

// ============================================================================
// 内部方法测试
// ============================================================================

// TestLRU2Cache_InternalGet 测试内部 getFromLevel 方法
func TestLRU2Cache_InternalGet(t *testing.T) {
	cache := New(1, 5, 5, time.Minute, nil)
	defer cache.Close()

	// 向一级缓存添加一个项
	idx := cache.keyToBucketIndex("test-key")
	cache.buckets[idx][0].put("test-key", testValue("test-value"), now()+int64(time.Hour), nil)

	// 从一级缓存获取
	entry := cache.getFromLevel("test-key", idx, 0)
	if entry == nil || entry.value != testValue("test-value") {
		t.Errorf("getFromLevel failed to retrieve from level 0")
	}

	// 向二级缓存添加一个项
	cache.buckets[idx][1].put("test-key2", testValue("test-value2"), now()+int64(time.Hour), nil)

	// 从二级缓存获取
	entry = cache.getFromLevel("test-key2", idx, 1)
	if entry == nil || entry.value != testValue("test-value2") {
		t.Errorf("getFromLevel failed to retrieve from level 1")
	}

	// 测试获取不存在的键
	entry = cache.getFromLevel("nonexistent", idx, 0)
	if entry != nil {
		t.Errorf("getFromLevel should return nil for nonexistent key")
	}

	// 测试过期项
	cache.buckets[idx][0].put("expired", testValue("value"), now()-1000, nil)
	entry = cache.getFromLevel("expired", idx, 0)
	if entry != nil {
		t.Errorf("getFromLevel should return nil for expired key")
	}
}

// TestLRU2Cache_InternalDelete 测试内部 delete 方法
func TestLRU2Cache_InternalDelete(t *testing.T) {
	var evictedKeys []string
	onEvicted := func(key string, value common.Value) {
		evictedKeys = append(evictedKeys, key)
	}

	cache := New(1, 5, 5, time.Minute, onEvicted)
	defer cache.Close()

	// 向一级缓存添加一个项
	idx := cache.keyToBucketIndex("test-key")
	cache.buckets[idx][0].put("test-key", testValue("test-value"), now()+int64(time.Hour), nil)

	// 向二级缓存添加一个项
	cache.buckets[idx][1].put("test-key2", testValue("test-value2"), now()+int64(time.Hour), nil)

	// 删除一级缓存中的项
	deleted := cache.delete("test-key", idx)
	if !deleted {
		t.Errorf("delete should return true for existing key")
	}

	// 验证淘汰回调被调用
	if len(evictedKeys) != 1 || evictedKeys[0] != "test-key" {
		t.Errorf("OnEvicted callback not called correctly, got %v", evictedKeys)
	}

	// 重置回调记录
	evictedKeys = nil

	// 删除二级缓存中的项
	deleted = cache.delete("test-key2", idx)
	if !deleted {
		t.Errorf("delete should return true for existing key in level 1")
	}

	// 验证淘汰回调被调用
	if len(evictedKeys) != 1 || evictedKeys[0] != "test-key2" {
		t.Errorf("OnEvicted callback not called correctly, got %v", evictedKeys)
	}

	// 测试删除不存在的键
	deleted = cache.delete("nonexistent", idx)
	if deleted {
		t.Errorf("delete should return false for nonexistent key")
	}
}

// ============================================================================
// 工具函数测试
// ============================================================================

// TestHashBKRD 测试 BKRD 哈希函数
func TestHashBKRD(t *testing.T) {
	// 测试哈希一致性
	hash1 := hashBKRD("test-key")
	hash2 := hashBKRD("test-key")
	if hash1 != hash2 {
		t.Error("相同键的哈希值应相同")
	}

	// 测试不同键产生不同哈希（可能有碰撞，但概率低）
	hash3 := hashBKRD("different-key")
	if hash1 == hash3 {
		t.Log("Warning: hash collision detected (rare but possible)")
	}

	// 测试空字符串
	hashEmpty := hashBKRD("")
	if hashEmpty != 0 {
		t.Logf("Empty string hash: %d", hashEmpty)
	}
}

// TestMaskOfNextPowOf2 测试 2 的幂次方掩码计算
func TestMaskOfNextPowOf2(t *testing.T) {
	tests := []struct {
		input    uint16
		expected uint16
	}{
		{1, 0},    // 2^0 - 1 = 0
		{2, 1},    // 2^1 - 1 = 1
		{3, 3},    // next pow of 2 is 4, 4-1=3
		{4, 3},    // 2^2 - 1 = 3
		{5, 7},    // next pow of 2 is 8, 8-1=7
		{8, 7},    // 2^3 - 1 = 7
		{9, 15},   // next pow of 2 is 16, 16-1=15
		{16, 15},  // 2^4 - 1 = 15
		{17, 31},  // next pow of 2 is 32, 32-1=31
		{100, 127},
		{256, 255},
	}

	for _, tt := range tests {
		result := maskOfNextPowOf2(tt.input)
		if result != tt.expected {
			t.Errorf("maskOfNextPowOf2(%d) = %d, expected %d", tt.input, result, tt.expected)
		}
	}
}

// ============================================================================
// 性能基准测试
// ============================================================================

// BenchmarkCacheBucket_Put 测试缓存桶 Put 性能
func BenchmarkCacheBucket_Put(b *testing.B) {
	bucket := createCache(1000)
	onEvicted := func(key string, value common.Value) {}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		bucket.put(key, testValue("value"), now()+int64(time.Hour), onEvicted)
	}
}

// BenchmarkCacheBucket_Get 测试缓存桶 Get 性能
func BenchmarkCacheBucket_Get(b *testing.B) {
	bucket := createCache(1000)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key%d", i)
		bucket.put(key, testValue("value"), now()+int64(time.Hour), nil)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%1000)
		bucket.get(key)
	}
}

// BenchmarkLRU2Cache_Set 测试缓存 Set 性能
func BenchmarkLRU2Cache_Set(b *testing.B) {
	cache := New(16, 1000, 2000, time.Minute, nil)
	defer cache.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		cache.Set(key, testValue("value"))
	}
}

// BenchmarkLRU2Cache_Get 测试缓存 Get 性能
func BenchmarkLRU2Cache_Get(b *testing.B) {
	cache := New(16, 1000, 2000, time.Minute, nil)
	defer cache.Close()

	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key%d", i)
		cache.Set(key, testValue("value"))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%10000)
		cache.Get(key)
	}
}

// BenchmarkLRU2Cache_Mixed 测试混合操作性能
func BenchmarkLRU2Cache_Mixed(b *testing.B) {
	cache := New(16, 1000, 2000, time.Minute, nil)
	defer cache.Close()

	// 预填充
	for i := 0; i < 5000; i++ {
		cache.Set(fmt.Sprintf("key%d", i), testValue(fmt.Sprintf("value%d", i)))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i%10000)
			// 75% Get, 25% Set
			if i%4 != 0 {
				cache.Get(key)
			} else {
				cache.Set(key, testValue("new-value"))
			}
			i++
		}
	})
}

// BenchmarkLRU2Cache_ConcurrentSet 测试并发 Set 性能
func BenchmarkLRU2Cache_ConcurrentSet(b *testing.B) {
	cache := New(16, 1000, 2000, time.Minute, nil)
	defer cache.Close()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i)
			cache.Set(key, testValue("value"))
			i++
		}
	})
}

// BenchmarkLRU2Cache_ConcurrentGet 测试并发 Get 性能
func BenchmarkLRU2Cache_ConcurrentGet(b *testing.B) {
	cache := New(16, 1000, 2000, time.Minute, nil)
	defer cache.Close()

	for i := 0; i < 10000; i++ {
		cache.Set(fmt.Sprintf("key%d", i), testValue("value"))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i%10000)
			cache.Get(key)
			i++
		}
	})
}

// ============================================================================
// 辅助函数
// ============================================================================

// contains 检查切片是否包含指定字符串
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// BenchmarkLRU2Cache_DifferentBucketCounts 测试不同桶数量的性能
func BenchmarkLRU2Cache_DifferentBucketCounts(b *testing.B) {
	bucketCounts := []uint16{1, 4, 16, 64, 256}

	for _, count := range bucketCounts {
		b.Run(fmt.Sprintf("Buckets%d", count), func(b *testing.B) {
			cache := New(count, 100, 200, time.Minute, nil)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := fmt.Sprintf("key%d", i)
				cache.Set(key, testValue("value"))
			}

			cache.Close()
		})
	}
}
