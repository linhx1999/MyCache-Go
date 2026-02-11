package consistenthash

import (
	"math"
	"sort"
	"sync/atomic"
	"time"
)

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
