package store

import (
	"testing"
)

// TestLRU_NilOnEvicted 验证 nil OnEvicted 使用空函数
func TestLRU_NilOnEvicted(t *testing.T) {
	opts := Options{MaxBytes: 1024, OnEvicted: nil}
	cache := newLRUCache(opts)
	defer cache.Close()

	// 添加并删除元素，不应该 panic
	cache.Set("key1", testValue("value1"))
	cache.Delete("key1")

	// 清空缓存，不应该 panic
	cache.Set("key2", testValue("value2"))
	cache.Clear()
}

// TestLRU_OnEvictedCallback 验证 OnEvicted 回调被正确调用
func TestLRU_OnEvictedCallback(t *testing.T) {
	evictedKeys := make(map[string]string)
	opts := Options{
		MaxBytes: 20, // 很小的限制，确保触发淘汰
		OnEvicted: func(key string, value Value) {
			evictedKeys[key] = string(value.(testValue))
		},
	}
	cache := newLRUCache(opts)
	defer cache.Close()

	// 添加多个项目，触发淘汰（每个项目约 9 bytes：4 bytes key + 5 bytes value）
	cache.Set("key1", testValue("val1")) // 9 bytes
	cache.Set("key2", testValue("val2")) // 9 bytes
	cache.Set("key3", testValue("val3")) // 9 bytes，总共 27 bytes > 20，触发淘汰

	// 验证有项目被淘汰
	if len(evictedKeys) == 0 {
		t.Error("Expected some keys to be evicted")
	}
}

// TestLRU_ClearWithOnEvicted 验证 Clear 调用 OnEvicted
func TestLRU_ClearWithOnEvicted(t *testing.T) {
	evictedKeys := make(map[string]string)
	opts := Options{
		MaxBytes: 1024,
		OnEvicted: func(key string, value Value) {
			evictedKeys[key] = string(value.(testValue))
		},
	}
	cache := newLRUCache(opts)
	defer cache.Close()

	// 添加项目
	cache.Set("key1", testValue("value1"))
	cache.Set("key2", testValue("value2"))

	// 清空缓存
	cache.Clear()

	// 验证所有项目都调用了回调
	if len(evictedKeys) != 2 {
		t.Errorf("Expected 2 evicted keys, got %d", len(evictedKeys))
	}
	if _, ok := evictedKeys["key1"]; !ok {
		t.Error("key1 should be evicted")
	}
	if _, ok := evictedKeys["key2"]; !ok {
		t.Error("key2 should be evicted")
	}
}

// TestLRU2_NilOnEvicted 验证 LRU2 的 nil OnEvicted 处理
func TestLRU2_NilOnEvicted(t *testing.T) {
	opts := Options{OnEvicted: nil}
	store := newLRU2Cache(opts)
	defer store.Close()

	// 添加和删除操作，不应该 panic
	store.Set("key1", testValue("value1"))
	store.Delete("key1")
}

// TestLRU2_OnEvictedCallback 验证 LRU2 的 OnEvicted 回调
func TestLRU2_OnEvictedCallback(t *testing.T) {
	evictedKeys := make(map[string]string)
	opts := Options{
		BucketCount:  4,
		CapPerBucket: 2,
		OnEvicted: func(key string, value Value) {
			evictedKeys[key] = string(value.(testValue))
		},
	}
	store := newLRU2Cache(opts)
	defer store.Close()

	// 添加项目到满了触发淘汰
	for i := 0; i < 10; i++ {
		store.Set(string(rune('a'+i)), testValue("value"+string(rune('0'+i))))
	}

	// 验证有项目被淘汰
	if len(evictedKeys) == 0 {
		t.Error("Expected some keys to be evicted in LRU2")
	}
}

// TestNoopOnEvicted 验证空函数不会 panic
func TestNoopOnEvicted(t *testing.T) {
	// 直接调用空函数，不应该 panic
	noopOnEvicted("test", testValue("value"))
}
