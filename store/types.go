package store

import (
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
	"github.com/linhx1999/MyCache-Go/store/lru"
	"github.com/linhx1999/MyCache-Go/store/lru2"
)

// Value 缓存值接口（类型别名，向后兼容）
type Value = common.Value

// Store 缓存接口
type Store interface {
	Get(key string) (Value, bool)
	Set(key string, value Value) error
	SetWithExpiration(key string, value Value, expiration time.Duration) error
	Delete(key string) bool
	Clear()
	Len() int
	Close()
}

// CacheType 缓存类型
type CacheType string

const (
	LRU  CacheType = "lru"
	LRU2 CacheType = "lru2"
)

// Options 通用缓存配置选项
type Options struct {
	MaxBytes        int64                        // 最大的缓存字节数（用于 lru）
	BucketCount     uint16                       // 缓存的桶数量（用于 lru-2）
	CapPerBucket    uint16                       // 每个桶的容量（用于 lru-2）
	Level2Cap       uint16                       // lru-2 中二级缓存的容量（用于 lru-2）
	CleanupInterval time.Duration
	OnEvicted       func(key string, value Value)
}

// NewStore 根据选项创建缓存实例
func NewStore(cacheType CacheType, opts Options) Store {
	switch cacheType {
	case LRU:
		return lru.New(opts.MaxBytes, opts.CleanupInterval, opts.OnEvicted)
	case LRU2:
		return lru2.New(opts.BucketCount, opts.CapPerBucket, opts.Level2Cap, opts.CleanupInterval, opts.OnEvicted)
	default:
		return lru2.New(opts.BucketCount, opts.CapPerBucket, opts.Level2Cap, opts.CleanupInterval, opts.OnEvicted)
	}
}
