package mycache

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/linhx1999/MyCache-Go/singleflight"
)

var (
	groupsMu sync.RWMutex
	groups   = make(map[string]*Group)
)

// ErrKeyRequired 键不能为空错误
var ErrKeyRequired = errors.New("cache: key is required")

// ErrValueRequired 值不能为空错误
var ErrValueRequired = errors.New("cache: value is required")

// ErrGroupClosed 组已关闭错误
var ErrGroupClosed = errors.New("cache: group is closed")

// DataSource 数据源接口，用于从外部数据源加载数据
type DataSource interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// DataSourceFunc 函数类型实现 DataSource 接口
type DataSourceFunc func(ctx context.Context, key string) ([]byte, error)

// Get 实现 DataSource 接口
func (f DataSourceFunc) Get(ctx context.Context, key string) ([]byte, error) {
	return f(ctx, key)
}

// Group 是一个缓存命名空间，支持分布式缓存和数据源加载
//
// Group 是 MyCache 的核心组件，提供以下功能：
//   - 缓存命名空间隔离：每个 Group 有独立的名称和缓存空间
//   - 分布式缓存支持：通过 PeerPicker 与其他节点同步数据
//   - 数据源回退机制：缓存未命中时通过 DataSource 从数据源加载
//   - 防缓存击穿：使用 SingleFlight 确保相同 key 的并发请求只加载一次
//   - 缓存过期管理：支持 TTL 过期时间
//   - 统计信息跟踪：记录命中率、加载耗时等指标
//
// 数据加载流程：
//
//	Get(key) → 本地缓存命中 → 返回数据
//	         ↓ 未命中
//	    使用 SingleFlight 加载（防止重复请求）
//	         ↓
//	    尝试从远程节点获取（通过 PeerPicker）
//	         ↓ 未命中
//	    调用 DataSource 回调从数据源加载
//	         ↓
//	    存入本地缓存并返回
//
// 数据同步机制：
//
//	Set/Delete 操作会异步同步到其他节点（使用 from_peer 标记避免循环同步）
//
// 并发安全：
//   - 使用 atomic 操作管理 closed 状态
//   - localCache 内部使用读写锁保护
//   - loader (SingleFlight) 确保并发安全
type Group struct {
	name       string              // 组名，用于标识和隔离不同的缓存空间
	dataSource DataSource          // 数据源，缓存未命中时从这里加载数据
	localCache *Cache              // 本地缓存实例，存储实际数据
	peers      PeerPicker          // 节点选择器，用于分布式缓存中的节点路由
	loader     *singleflight.Group // SingleFlight 实例，防止缓存击穿
	expiration time.Duration       // 缓存过期时间，0 表示永不过期
	closed     atomic.Int32        // 原子变量，标记组是否已关闭（0=运行中，1=已关闭）
	stats      groupStats          // 统计信息，记录命中率、加载次数等指标
}

// groupStats 保存组的统计信息
type groupStats struct {
	loads        atomic.Int64 // 加载次数
	localHits    atomic.Int64 // 本地缓存命中次数
	localMisses  atomic.Int64 // 本地缓存未命中次数
	peerHits     atomic.Int64 // 从对等节点获取成功次数
	peerMisses   atomic.Int64 // 从对等节点获取失败次数
	loaderHits   atomic.Int64 // 从加载器获取成功次数
	loaderErrors atomic.Int64 // 从加载器获取失败次数
	loadDuration atomic.Int64 // 加载总耗时（纳秒）
}

// GroupOption 定义Group的配置选项
type GroupOption func(*Group)

// WithExpiration 设置缓存过期时间
func WithExpiration(d time.Duration) GroupOption {
	return func(g *Group) {
		g.expiration = d
	}
}

// WithPeers 设置分布式节点
func WithPeers(peers PeerPicker) GroupOption {
	return func(g *Group) {
		g.peers = peers
	}
}

// WithCacheOptions 设置缓存选项
func WithCacheOptions(opts CacheOptions) GroupOption {
	return func(g *Group) {
		g.localCache = NewCache(opts)
	}
}

// NewGroup 创建一个新的 Group 实例
func NewGroup(name string, cacheBytes int64, dataSource DataSource, opts ...GroupOption) *Group {
	if dataSource == nil {
		panic("nil DataSource")
	}

	// 创建默认缓存选项
	cacheOpts := DefaultCacheOptions()
	cacheOpts.MaxBytes = cacheBytes

	g := &Group{
		name:       name,
		dataSource: dataSource,
		localCache: NewCache(cacheOpts),
		loader:     &singleflight.Group{},
	}

	// 应用选项
	for _, opt := range opts {
		opt(g)
	}

	// 注册到全局组映射
	groupsMu.Lock()
	defer groupsMu.Unlock()

	if _, exists := groups[name]; exists {
		log.Printf("[Group] %s already exists, will be replaced", name)
	}

	groups[name] = g
	log.Printf("[Group] Created [%s] with cacheBytes=%d, expiration=%v", name, cacheBytes, g.expiration)

	return g
}

// GetGroup 获取指定名称的组
func GetGroup(name string) *Group {
	groupsMu.RLock()
	defer groupsMu.RUnlock()
	return groups[name]
}

// Get 从缓存获取数据
func (g *Group) Get(ctx context.Context, key string) (ByteView, error) {
	// 检查组是否已关闭
	if g.closed.Load() == 1 {
		return ByteView{}, ErrGroupClosed
	}

	if key == "" {
		return ByteView{}, ErrKeyRequired
	}

	// 从本地缓存获取
	view, ok := g.localCache.Get(ctx, key)
	if ok {
		g.stats.localHits.Add(1)
		return view, nil
	}

	g.stats.localMisses.Add(1)

	// 尝试从其他节点获取或加载
	return g.loadOnce(ctx, key)
}

// Set 设置缓存值
func (g *Group) Set(ctx context.Context, key string, value []byte) error {
	// 检查组是否已关闭
	if g.closed.Load() == 1 {
		return ErrGroupClosed
	}

	if key == "" {
		return ErrKeyRequired
	}
	if len(value) == 0 {
		return ErrValueRequired
	}

	// 检查是否是从其他节点同步过来的请求
	isPeerRequest := ctx.Value("from_peer") != nil

	// 创建缓存视图
	view := ByteView{b: cloneBytes(value)}

	// 设置到本地缓存
	if g.expiration > 0 {
		g.localCache.AddWithExpiration(key, view, time.Now().Add(g.expiration))
	} else {
		g.localCache.Add(key, view)
	}

	// 如果不是从其他节点同步过来的请求，且启用了分布式模式，同步到其他节点
	if !isPeerRequest && g.peers != nil {
		go g.syncToPeers(ctx, "set", key, value)
	}

	return nil
}

// Delete 删除缓存值
func (g *Group) Delete(ctx context.Context, key string) error {
	// 检查组是否已关闭
	if g.closed.Load() == 1 {
		return ErrGroupClosed
	}

	if key == "" {
		return ErrKeyRequired
	}

	// 从本地缓存删除
	g.localCache.Delete(key)

	// 检查是否是从其他节点同步过来的请求
	isPeerRequest := ctx.Value("from_peer") != nil

	// 如果不是从其他节点同步过来的请求，且启用了分布式模式，同步到其他节点
	if !isPeerRequest && g.peers != nil {
		go g.syncToPeers(ctx, "delete", key, nil)
	}

	return nil
}

// syncToPeers 同步操作到其他节点
func (g *Group) syncToPeers(ctx context.Context, op string, key string, value []byte) {
	if g.peers == nil {
		return
	}

	// 选择对等节点
	peer, ok, isSelf := g.peers.PickPeer(key)
	if !ok || isSelf {
		return
	}

	// 创建同步请求上下文
	syncCtx := context.WithValue(context.Background(), "from_peer", true)

	var err error
	switch op {
	case "set":
		err = peer.Set(syncCtx, g.name, key, value)
	case "delete":
		_, err = peer.Delete(g.name, key)
	}

	if err != nil {
		log.Printf("[KamaCache] failed to sync %s to peer: %v", op, err)
	}
}

// Clear 清空缓存
func (g *Group) Clear() {
	// 检查组是否已关闭
	if g.closed.Load() == 1 {
		return
	}

	g.localCache.Clear()
	log.Printf("[KamaCache] cleared cache for group [%s]", g.name)
}

// Close 关闭组并释放资源
func (g *Group) Close() error {
	// 如果已经关闭，直接返回
	if !g.closed.CompareAndSwap(0, 1) {
		return nil
	}

	// 关闭本地缓存
	if g.localCache != nil {
		g.localCache.Close()
	}

	// 从全局组映射中移除
	groupsMu.Lock()
	delete(groups, g.name)
	groupsMu.Unlock()

	log.Printf("[KamaCache] closed cache group [%s]", g.name)
	return nil
}

// loadOnce 使用 SingleFlight 机制加载数据，防止缓存击穿
// 该方法确保相同 key 的并发请求只会执行一次加载操作
// 加载完成后会将数据存入本地缓存
func (g *Group) loadOnce(ctx context.Context, key string) (ByteView, error) {
	startTime := time.Now()

	// 使用 SingleFlight.Do 确保并发请求只执行一次加载
	// Do 方法会阻塞所有相同 key 的请求，直到第一个请求完成
	// 所有等待的请求将共享同一个结果
	result, err := g.loader.Do(key, func() (interface{}, error) {
		return g.fetchData(ctx, key)
	})

	// 记录加载统计信息
	duration := time.Since(startTime).Nanoseconds()
	g.stats.loadDuration.Add(duration)
	g.stats.loads.Add(1)

	if err != nil {
		g.stats.loaderErrors.Add(1)
		return ByteView{}, err
	}

	// 类型断言：将 interface{} 转换为 ByteView
	view, ok := result.(ByteView)
	if !ok {
		g.stats.loaderErrors.Add(1)
		return ByteView{}, fmt.Errorf("unexpected type: %T", result)
	}

	// 将加载的数据存入本地缓存，便于下次快速访问
	g.saveToCache(key, view)

	return view, nil
}

// saveToCache 将数据存入本地缓存
func (g *Group) saveToCache(key string, view ByteView) {
	if g.expiration > 0 {
		expirationTime := time.Now().Add(g.expiration)
		g.localCache.AddWithExpiration(key, view, expirationTime)
	} else {
		g.localCache.Add(key, view)
	}
}

// fetchData 从远程节点或数据源获取数据
// 首先尝试从远程节点获取，失败则从本地数据源加载
func (g *Group) fetchData(ctx context.Context, key string) (value ByteView, err error) {
	// 尝试从远程节点获取
	if g.peers != nil {
		peer, ok, isSelf := g.peers.PickPeer(key)
		if ok && !isSelf {
			value, err := g.fetchFromPeer(ctx, peer, key)
			if err == nil {
				g.stats.peerHits.Add(1)
				return value, nil
			}

			g.stats.peerMisses.Add(1)
			log.Printf("[KamaCache] failed to get from peer: %v", err)
		}
	}

	// 从数据源加载
	bytes, err := g.dataSource.Get(ctx, key)
	if err != nil {
		return ByteView{}, fmt.Errorf("failed to get data: %w", err)
	}

	g.stats.loaderHits.Add(1)
	return ByteView{b: cloneBytes(bytes)}, nil
}

// fetchFromPeer 从其他节点获取数据
func (g *Group) fetchFromPeer(ctx context.Context, peer Peer, key string) (ByteView, error) {
	bytes, err := peer.Get(g.name, key)
	if err != nil {
		return ByteView{}, fmt.Errorf("failed to get from peer: %w", err)
	}
	return ByteView{b: bytes}, nil
}

// RegisterPeers 注册PeerPicker
func (g *Group) RegisterPeers(peers PeerPicker) {
	if g.peers != nil {
		panic("RegisterPeers called more than once")
	}
	g.peers = peers
	log.Printf("[KamaCache] registered peers for group [%s]", g.name)
}

// Stats 返回缓存统计信息
func (g *Group) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"name":          g.name,
		"closed":        g.closed.Load() == 1,
		"expiration":    g.expiration,
		"loads":         g.stats.loads.Load(),
		"local_hits":    g.stats.localHits.Load(),
		"local_misses":  g.stats.localMisses.Load(),
		"peer_hits":     g.stats.peerHits.Load(),
		"peer_misses":   g.stats.peerMisses.Load(),
		"loader_hits":   g.stats.loaderHits.Load(),
		"loader_errors": g.stats.loaderErrors.Load(),
	}

	// 计算各种命中率
	totalGets := stats["local_hits"].(int64) + stats["local_misses"].(int64)
	if totalGets > 0 {
		stats["hit_rate"] = float64(stats["local_hits"].(int64)) / float64(totalGets)
	}

	totalLoads := stats["loads"].(int64)
	if totalLoads > 0 {
		stats["avg_load_time_ms"] = float64(g.stats.loadDuration.Load()) / float64(totalLoads) / float64(time.Millisecond)
	}

	// 添加缓存大小
	if g.localCache != nil {
		cacheStats := g.localCache.Stats()
		for k, v := range cacheStats {
			stats["cache_"+k] = v
		}
	}

	return stats
}

// ListGroups 返回所有缓存组的名称
func ListGroups() []string {
	groupsMu.RLock()
	defer groupsMu.RUnlock()

	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}

	return names
}

// DestroyGroup 销毁指定名称的缓存组
func DestroyGroup(name string) bool {
	groupsMu.Lock()
	defer groupsMu.Unlock()

	if g, exists := groups[name]; exists {
		g.Close()
		delete(groups, name)
		log.Printf("[KamaCache] destroyed cache group [%s]", name)
		return true
	}

	return false
}

// DestroyAllGroups 销毁所有缓存组
func DestroyAllGroups() {
	groupsMu.Lock()
	defer groupsMu.Unlock()

	for name, g := range groups {
		g.Close()
		delete(groups, name)
		log.Printf("[KamaCache] destroyed cache group [%s]", name)
	}
}
