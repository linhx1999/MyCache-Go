package lru2

import (
	"sync"
	"time"

	"github.com/linhx1999/MyCache-Go/store/common"
)

// New 创建一个新的 LRU2Cache 缓存实例
func New(bucketCount, capPerBucket, level2Cap uint16, cleanupInterval time.Duration, onEvicted func(string, common.Value)) *LRU2Cache {
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
	c := &LRU2Cache{
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
