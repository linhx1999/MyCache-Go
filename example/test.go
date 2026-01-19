package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	myCache "github.com/linhx1999/MyCache-Go"
)

const (
	serviceName   = "my-cache"
	etcdEndpoint  = "localhost:2379"
	groupName     = "test"
	cacheMaxBytes = 2 << 20 // 2MB
	dialTimeout   = 5 * time.Second
	registryWait  = 5 * time.Second
	peerReadyWait = 30 * time.Second
)

func main() {
	// 定义命令行参数
	var nodeID string
	var ip string
	var port string

	// 解析命令行参数
	flag.StringVar(&nodeID, "node", "A", "节点标识符")
	flag.StringVar(&ip, "addr", "127.0.0.1", "节点地址")
	flag.StringVar(&port, "port", "8001", "节点端口")
	flag.Parse()

	addr := fmt.Sprintf("%s:%s", ip, port)

	log.Printf("[节点 %s ] 启动，地址: %s", nodeID, addr)

	node := createServer(addr, nodeID)
	picker := createPeerPicker(addr, nodeID)
	group := createCacheGroup(nodeID)
	group.RegisterPeers(picker)

	startServer(node, nodeID)
	waitForRegistry(nodeID)
	setLocalData(group, nodeID)

	fmt.Printf("\n[节点%s] 等待其他节点准备就绪...\n", nodeID)
	time.Sleep(peerReadyWait)

	printDiscoveredPeers(picker)
	testLocalDataRetrieval(group, nodeID)
	testRemoteDataRetrieval(group, nodeID)

	keepAlive()
}

// createServer 创建 gRPC 服务器
func createServer(addr, nodeID string) *myCache.Server {
	node, err := myCache.NewServer(
		addr,
		serviceName,
		myCache.WithEtcdEndpoints([]string{etcdEndpoint}),
		myCache.WithDialTimeout(dialTimeout),
	)
	if err != nil {
		log.Fatalf("[节点 %s ] 创建失败: %v", nodeID, err)
	}
	return node
}

// createPeerPicker 创建节点选择器
func createPeerPicker(addr, nodeID string) *myCache.ClientPicker {
	picker, err := myCache.NewClientPicker(addr)
	if err != nil {
		log.Fatalf("[节点 %s ] 创建失败: %v", nodeID, err)
	}
	return picker
}

// createCacheGroup 创建缓存组
//
// 该函数为每个节点创建独立的缓存组，并定义数据源加载逻辑。
// 当缓存未命中且无法从远程节点获取数据时，会调用 DataSource 回调从数据源加载数据。
//
// 参数:
//   - nodeID: 节点标识符（如 "A"、"B"、"C"），用于区分不同的缓存节点
//
// 返回:
//   - *myCache.Group: 已初始化的缓存组实例
//
// 核心逻辑:
//  1. 定义 DataSource 回调函数：实现缓存未命中时的数据源加载逻辑
//  2. 创建 Group 实例：使用全局配置的组名和最大缓存容量
//
// 注意事项:
//   - DataSource 是分布式缓存的数据源回退机制
//   - 本示例返回模拟数据，实际应用中应从数据库或 API 加载真实数据
func createCacheGroup(nodeID string) *myCache.Group {
	// 定义数据源加载回调（DataSource 接口）
	dataSource := myCache.DataSourceFunc(func(ctx context.Context, key string) ([]byte, error) {
		log.Printf("[节点 %s ] 触发数据源加载: key = %s", nodeID, key)
		return []byte(fmt.Sprintf("节点 %s 的数据源值", nodeID)), nil
	})

	// 创建缓存组：传入组名、最大容量、数据源回调
	return myCache.NewGroup(groupName, cacheMaxBytes, dataSource)
}

// startServer 在 goroutine 中启动服务器
func startServer(node *myCache.Server, nodeID string) {
	go func() {
		log.Printf("[节点 %s ] 开始启动服务...", nodeID)
		if err := node.Start(); err != nil {
			log.Fatalf("[节点 %s ] 启动节点失败: %v", nodeID, err)
		}
	}()
}

// waitForRegistry 等待服务注册完成
func waitForRegistry(nodeID string) {
	log.Printf("[节点 %s ] 等待节点注册...", nodeID)
	time.Sleep(registryWait)
}

// setLocalData 设置本地节点的数据
func setLocalData(group *myCache.Group, nodeID string) {
	localKey := fmt.Sprintf("key_%s", nodeID)
	localValue := []byte(fmt.Sprintf("这是节点 %s 的数据", nodeID))

	fmt.Printf("\n=== 节点 %s ：设置本地数据 ===\n", nodeID)
	if err := group.Set(context.Background(), localKey, localValue); err != nil {
		log.Fatalf("[节点 %s ] 设置本地数据失败: %v", nodeID, err)
	}
	fmt.Printf("节点 %s : 设置键 %s 成功\n", nodeID, localKey)
}

// printDiscoveredPeers 打印已发现的节点
func printDiscoveredPeers(picker *myCache.ClientPicker) {
	fmt.Println()
	picker.PrintPeers()
}

// testLocalDataRetrieval 测试获取本地数据
func testLocalDataRetrieval(group *myCache.Group, nodeID string) {
	localKey := fmt.Sprintf("key_%s", nodeID)
	ctx := context.Background()

	fmt.Printf("\n=== 节点%s：获取本地数据 ===\n", nodeID)
	fmt.Println("直接查询本地缓存...")

	stats := group.Stats()
	fmt.Printf("缓存统计: %+v\n", stats)

	if val, err := group.Get(ctx, localKey); err == nil {
		fmt.Printf("节点%s: 获取本地键 %s 成功: %s\n", nodeID, localKey, val.String())
	} else {
		fmt.Printf("节点%s: 获取本地键失败: %v\n", nodeID, err)
	}
}

// testRemoteDataRetrieval 测试获取远程节点数据
func testRemoteDataRetrieval(group *myCache.Group, nodeID string) {
	otherKeys := []string{"key_A", "key_B", "key_C"}
	ctx := context.Background()

	for _, key := range otherKeys {
		if key == fmt.Sprintf("key_%s", nodeID) {
			continue
		}
		fmt.Printf("\n=== 节点%s：尝试获取远程数据 %s ===\n", nodeID, key)
		log.Printf("[节点%s] 开始查找键 %s 的远程节点", nodeID, key)

		if val, err := group.Get(ctx, key); err == nil {
			fmt.Printf("节点%s: 获取远程键 %s 成功: %s\n", nodeID, key, val.String())
		} else {
			fmt.Printf("节点%s: 获取远程键失败: %v\n", nodeID, err)
		}
	}
}

// keepAlive 保持程序运行
func keepAlive() {
	select {}
}
