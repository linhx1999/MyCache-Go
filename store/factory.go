package store

import "time"

// noopOnEvicted 空淘汰回调函数，用作默认值以避免 nil 检查
func noopOnEvicted(key string, value Value) {}

// NewOptions 创建带有默认值的缓存配置选项
func NewOptions() Options {
	return Options{
		MaxBytes:        8192,
		BucketCount:     16,
		CapPerBucket:    512,
		Level2Cap:       256,
		CleanupInterval: time.Minute,
		OnEvicted:       noopOnEvicted,
	}
}

// NewStore 创建缓存存储实例
// 根据指定的缓存类型返回对应的缓存实现
func NewStore(cacheType CacheType, opts Options) Store {
	switch cacheType {
	case LRU2:
		return newLRU2Cache(opts)
	case LRU:
		return newLRUCache(opts)
	default:
		return newLRUCache(opts)
	}
}
