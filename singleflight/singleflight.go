package singleflight

import (
	"sync"
)

// call 代表一个正在执行或已完成的请求
type call struct {
	waitGroup sync.WaitGroup // 用于阻塞等待相同 key 的并发请求
	value     interface{}    // 请求返回的结果值
	err       error          // 请求执行过程中发生的错误
}

// Group 用于管理并发请求，确保相同 key 的请求只执行一次
type Group struct {
	callsMap sync.Map // key -> *call，存储正在执行的请求
}

// Do 执行给定函数 fn，并确保对于相同的 key，在任意时刻只有一个 fn 正在执行
// 如果已有相同 key 的请求正在执行，则等待其完成并共享结果
func (g *Group) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	// 检查是否已有正在执行的请求
	if existingCall, ok := g.callsMap.Load(key); ok {
		c := existingCall.(*call)
		c.waitGroup.Wait()    // 等待正在执行的请求完成
		return c.value, c.err // 复用已完成的请求结果
	}

	// 没有正在执行的请求，创建新的请求
	c := &call{}
	c.waitGroup.Add(1)
	g.callsMap.Store(key, c) // 存储到 map 中，让其他相同 key 的请求能够发现

	// 执行函数并记录结果
	c.value, c.err = fn()
	c.waitGroup.Done() // 通知所有等待的请求，当前请求已完成

	// 请求完成后从 map 中移除，释放内存
	g.callsMap.Delete(key)

	return c.value, c.err
}
