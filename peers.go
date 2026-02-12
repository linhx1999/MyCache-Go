package mycache

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/linhx1999/MyCache-Go/consistenthash"
	"github.com/linhx1999/MyCache-Go/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const defaultSvcName = "kama-cache"

// PeerPicker 定义了peer选择器的接口
type PeerPicker interface {
	PickPeer(key string) (peer Peer, ok bool, self bool)
	Close() error
}

// Peer 定义了缓存节点的接口
type Peer interface {
	Get(group string, key string) ([]byte, error)
	Set(ctx context.Context, group string, key string, value []byte) error
	Delete(group string, key string) (bool, error)
	Close() error
}

// ClientPicker 实现了PeerPicker接口
type ClientPicker struct {
	selfAddr string                   // 本节点地址，用于识别自身，避免将请求路由到自己
	svcName  string                   // 服务名称，用于etcd中区分不同的缓存服务
	mu       sync.RWMutex             // 保护一致性哈希环和客户端映射的并发访问
	consHash *consistenthash.HashRing // 一致性哈希环，用于根据key选择目标节点
	clients  map[string]*Client       // 地址到gRPC客户端的映射，存储与其他节点的连接
	etcdCli  *clientv3.Client         // etcd客户端，用于服务发现和监听节点变化
	ctx      context.Context          // 上下文，用于控制服务发现goroutine的生命周期
	cancel   context.CancelFunc       // 取消函数，用于优雅关闭服务发现
}

// PickerOption 定义配置选项
type PickerOption func(*ClientPicker)

// WithServiceName 设置服务名称
func WithServiceName(name string) PickerOption {
	return func(p *ClientPicker) {
		p.svcName = name
	}
}

// PrintPeers 打印当前已发现的节点（仅用于调试）
func (p *ClientPicker) PrintPeers() {
	p.mu.RLock()
	defer p.mu.RUnlock()

	log.Printf("当前已发现的节点:")
	for addr := range p.clients {
		log.Printf("- %s", addr)
	}
}

// NewClientPicker 创建新的ClientPicker实例
func NewClientPicker(addr string, opts ...PickerOption) (*ClientPicker, error) {
	ctx, cancel := context.WithCancel(context.Background())
	picker := &ClientPicker{
		selfAddr: addr,
		svcName:  defaultSvcName,
		clients:  make(map[string]*Client),
		consHash: consistenthash.New(),
		ctx:      ctx,
		cancel:   cancel,
	}

	for _, opt := range opts {
		opt(picker)
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   registry.DefaultConfig.Endpoints,
		DialTimeout: registry.DefaultConfig.DialTimeout,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create etcd client: %v", err)
	}
	picker.etcdCli = cli

	// 启动服务发现
	if err := picker.startServiceDiscovery(); err != nil {
		cancel()
		cli.Close()
		return nil, err
	}

	return picker, nil
}

// startServiceDiscovery 启动服务发现
func (p *ClientPicker) startServiceDiscovery() error {
	// 先进行全量更新
	if err := p.fetchAllServices(); err != nil {
		return err
	}

	// 启动增量更新
	go p.watchServiceChanges()
	return nil
}

// watchServiceChanges 监听服务实例变化
func (p *ClientPicker) watchServiceChanges() {
	watcher := clientv3.NewWatcher(p.etcdCli)
	watchChan := watcher.Watch(p.ctx, "/services/"+p.svcName, clientv3.WithPrefix())

	for {
		select {
		case <-p.ctx.Done():
			watcher.Close()
			return
		case resp := <-watchChan:
			p.handleWatchEvents(resp.Events)
		}
	}
}

// handleWatchEvents 处理监听到的事件
func (p *ClientPicker) handleWatchEvents(events []*clientv3.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, event := range events {
		addr := string(event.Kv.Value)
		if addr == p.selfAddr {
			continue
		}

		switch event.Type {
		case clientv3.EventTypePut:
			if _, exists := p.clients[addr]; !exists {
				p.set(addr)
				log.Printf("[PeerPicker] New service discovered at %s", addr)
			}
		case clientv3.EventTypeDelete:
			if client, exists := p.clients[addr]; exists {
				client.Close()
				p.remove(addr)
				log.Printf("[PeerPicker] Service removed at %s", addr)
			}
		}
	}
}

// fetchAllServices 获取所有服务实例
func (p *ClientPicker) fetchAllServices() error {
	ctx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()

	resp, err := p.etcdCli.Get(ctx, "/services/"+p.svcName, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to get all services: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, kv := range resp.Kvs {
		addr := string(kv.Value)
		if addr != "" && addr != p.selfAddr {
			p.set(addr)
			log.Printf("[PeerPicker] Discovered service at %s", addr)
		}
	}
	return nil
}

// set 添加服务实例
func (p *ClientPicker) set(addr string) {
	if client, err := NewClient(addr, p.svcName, p.etcdCli); err == nil {
		p.consHash.Add(addr)
		p.clients[addr] = client
		log.Printf("[PeerPicker] Successfully created client for %s", addr)
	} else {
		log.Printf("[PeerPicker] ERROR: Failed to create client for %s: %v", addr, err)
	}
}

// remove 移除服务实例
func (p *ClientPicker) remove(addr string) {
	p.consHash.Remove(addr)
	delete(p.clients, addr)
}

// PickPeer 选择peer节点
func (p *ClientPicker) PickPeer(key string) (Peer, bool, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if addr := p.consHash.Get(key); addr != "" {
		if client, ok := p.clients[addr]; ok {
			return client, true, addr == p.selfAddr
		}
	}
	return nil, false, false
}

// Close 关闭所有资源
func (p *ClientPicker) Close() error {
	p.cancel()
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	for addr, client := range p.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close client %s: %v", addr, err))
		}
	}

	if err := p.etcdCli.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close etcd client: %v", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors while closing: %v", errs)
	}
	return nil
}

// parseAddrFromKey 从etcd key中解析地址
// func parseAddrFromKey(key, svcName string) string {
// 	prefix := fmt.Sprintf("/services/%s/", svcName)
// 	if strings.HasPrefix(key, prefix) {
// 		return strings.TrimPrefix(key, prefix)
// 	}
// 	return ""
// }
