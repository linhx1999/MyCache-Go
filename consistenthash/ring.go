package consistenthash

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// HashRing 一致性哈希实现
type HashRing struct {
	mu sync.RWMutex
	// 配置信息
	config *Config
	// 哈希环
	keys []int
	// 哈希环到节点的映射
	hashMap map[int]string
	// 节点到虚拟节点数量的映射
	nodeReplicas map[string]int
	// 节点负载统计
	nodeCounts map[string]int64
	// 总请求数
	totalRequests int64
}

// New 创建一致性哈希实例
func New(opts ...Option) *HashRing {
	r := &HashRing{
		config:       DefaultConfig,
		hashMap:      make(map[int]string),
		nodeReplicas: make(map[string]int),
		nodeCounts:   make(map[string]int64),
	}

	for _, opt := range opts {
		opt(r)
	}

	r.startBalancer() // 启动负载均衡器
	return r
}

// Add 添加节点
func (r *HashRing) Add(nodes ...string) error {
	if len(nodes) == 0 {
		return errors.New("no nodes provided")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, node := range nodes {
		if node == "" {
			continue
		}

		// 为节点添加虚拟节点
		r.addNode(node, r.config.DefaultReplicas)
	}

	// 重新排序
	sort.Ints(r.keys)
	return nil
}

// Remove 移除节点
func (r *HashRing) Remove(node string) error {
	if node == "" {
		return errors.New("invalid node")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	replicas := r.nodeReplicas[node]
	if replicas == 0 {
		return fmt.Errorf("node %s not found", node)
	}

	// 移除节点的所有虚拟节点
	for i := 0; i < replicas; i++ {
		hash := int(r.config.HashFunc([]byte(fmt.Sprintf("%s-%d", node, i))))
		delete(r.hashMap, hash)
		for j := 0; j < len(r.keys); j++ {
			if r.keys[j] == hash {
				r.keys = append(r.keys[:j], r.keys[j+1:]...)
				break
			}
		}
	}

	delete(r.nodeReplicas, node)
	delete(r.nodeCounts, node)
	return nil
}

// Get 获取节点
func (r *HashRing) Get(key string) string {
	if key == "" {
		return ""
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.keys) == 0 {
		return ""
	}

	hash := int(r.config.HashFunc([]byte(key)))
	// 二分查找
	idx := sort.Search(len(r.keys), func(i int) bool {
		return r.keys[i] >= hash
	})

	// 处理边界情况
	if idx == len(r.keys) {
		idx = 0
	}

	node := r.hashMap[r.keys[idx]]
	count := r.nodeCounts[node]
	r.nodeCounts[node] = count + 1
	atomic.AddInt64(&r.totalRequests, 1)

	return node
}

// addNode 添加节点的虚拟节点
func (r *HashRing) addNode(node string, replicas int) {
	for i := 0; i < replicas; i++ {
		hash := int(r.config.HashFunc([]byte(fmt.Sprintf("%s-%d", node, i))))
		r.keys = append(r.keys, hash)
		r.hashMap[hash] = node
	}
	r.nodeReplicas[node] = replicas
}
