package lru2

import (
	"sync/atomic"
	"time"
)

// 内部时钟和链表方向常量，用于减少 time.Now() 系统调用造成的性能开销
var (
	clock int64  = time.Now().UnixNano() // 全局缓存时钟（纳秒），后台协程每秒校准一次
	prev  uint16 = 0                     // 双向链表前驱方向索引（links[i][0] 表示前驱）
	next  uint16 = 1                     // 双向链表后继方向索引（links[i][1] 表示后继）
)

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
