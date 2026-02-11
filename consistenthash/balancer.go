package consistenthash

import (
	"math"
	"sort"
	"sync/atomic"
	"time"
)

// minSampleSize 触发负载重平衡的最小样本数量
// 避免在样本量不足时做出错误的调整决策
const minSampleSize = 1000

// checkAndRebalance 检查负载分布并在必要时重新平衡虚拟节点
//
// 算法逻辑：
// 1. 收集每个节点的请求次数，计算平均负载
// 2. 找出与平均负载偏差最大的节点（以比例计算）
// 3. 如果最大偏差超过阈值（如25%），触发重平衡
// 4. 重平衡策略：高负载节点减少虚拟节点，低负载节点增加虚拟节点
func (r *HashRing) checkAndRebalance() {
	// 样本量不足时不进行调整，避免误差过大
	totalRequests := atomic.LoadInt64(&r.totalRequests)
	if totalRequests < minSampleSize {
		return
	}

	// 计算平均每个节点应该处理的请求数
	avgLoad := float64(totalRequests) / float64(len(r.nodeReplicas))

	// 找出负载偏差最大的节点
	maxDeviationRatio := r.calculateMaxDeviation(avgLoad)

	// 当最大偏差超过配置的阈值时，触发重平衡
	if maxDeviationRatio > r.config.LoadBalanceThreshold {
		r.rebalanceNodes()
	}
}

// calculateMaxDeviation 计算所有节点中与平均负载的最大偏差比例
// deviation = |actual - expected| / expected
func (r *HashRing) calculateMaxDeviation(avgLoad float64) float64 {
	var maxDeviation float64

	for _, count := range r.nodeCounts {
		// 计算当前节点与平均负载的偏差比例
		deviation := math.Abs(float64(count)-avgLoad) / avgLoad
		if deviation > maxDeviation {
			maxDeviation = deviation
		}
	}

	return maxDeviation
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
