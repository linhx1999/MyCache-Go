package consistenthash

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
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

// Option 配置选项
type Option func(*HashRing)

// WithConfig 设置配置
func WithConfig(config *Config) Option {
	return func(r *HashRing) {
		r.config = config
	}
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

// checkAndRebalance 检查并重新平衡虚拟节点
func (r *HashRing) checkAndRebalance() {
	if atomic.LoadInt64(&r.totalRequests) < 1000 {
		return // 样本太少，不进行调整
	}

	// 计算负载情况
	avgLoad := float64(r.totalRequests) / float64(len(r.nodeReplicas))
	var maxDiff float64

	for _, count := range r.nodeCounts {
		diff := math.Abs(float64(count) - avgLoad)
		if diff/avgLoad > maxDiff {
			maxDiff = diff / avgLoad
		}
	}

	// 如果负载不均衡度超过阈值，调整虚拟节点
	if maxDiff > r.config.LoadBalanceThreshold {
		r.rebalanceNodes()
	}
}

// rebalanceNodes 重新平衡节点
func (r *HashRing) rebalanceNodes() {
	r.mu.Lock()
	defer r.mu.Unlock()

	avgLoad := float64(r.totalRequests) / float64(len(r.nodeReplicas))

	// 调整每个节点的虚拟节点数量
	for node, count := range r.nodeCounts {
		currentReplicas := r.nodeReplicas[node]
		loadRatio := float64(count) / avgLoad

		var newReplicas int
		if loadRatio > 1 {
			// 负载过高，减少虚拟节点
			newReplicas = int(float64(currentReplicas) / loadRatio)
		} else {
			// 负载过低，增加虚拟节点
			newReplicas = int(float64(currentReplicas) * (2 - loadRatio))
		}

		// 确保在限制范围内
		if newReplicas < r.config.MinReplicas {
			newReplicas = r.config.MinReplicas
		}
		if newReplicas > r.config.MaxReplicas {
			newReplicas = r.config.MaxReplicas
		}

		if newReplicas != currentReplicas {
			// 重新添加节点的虚拟节点
			if err := r.Remove(node); err != nil {
				continue // 如果移除失败，跳过这个节点
			}
			r.addNode(node, newReplicas)
		}
	}

	// 重置计数器
	for node := range r.nodeCounts {
		r.nodeCounts[node] = 0
	}
	atomic.StoreInt64(&r.totalRequests, 0)

	// 重新排序
	sort.Ints(r.keys)
}

// GetStats 获取负载统计信息
func (r *HashRing) GetStats() map[string]float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make(map[string]float64)
	total := atomic.LoadInt64(&r.totalRequests)
	if total == 0 {
		return stats
	}

	for node, count := range r.nodeCounts {
		stats[node] = float64(count) / float64(total)
	}
	return stats
}

// 将checkAndRebalance移到单独的goroutine中
func (r *HashRing) startBalancer() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for range ticker.C {
			r.checkAndRebalance()
		}
	}()
}
