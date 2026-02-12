package mycache

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	pb "github.com/linhx1999/MyCache-Go/pb"
	"github.com/linhx1999/MyCache-Go/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Server 定义缓存服务器
type Server struct {
	pb.UnimplementedCacheServiceServer
	addr       string           // 服务地址
	svcName    string           // 服务名称
	groups     *sync.Map        // 缓存组
	grpcServer *grpc.Server     // gRPC服务器
	etcdCli    *clientv3.Client // etcd客户端
	stopCh     chan error       // 停止信号
	opts       *ServerOptions   // 服务器选项
}

// ServerOptions 服务器配置选项
type ServerOptions struct {
	EtcdEndpoints []string      // etcd端点
	DialTimeout   time.Duration // 连接超时
	MaxMsgSize    int           // 最大消息大小
	TLS           bool          // 是否启用TLS
	CertFile      string        // 证书文件
	KeyFile       string        // 密钥文件
}

// DefaultServerOptions 默认配置
var DefaultServerOptions = &ServerOptions{
	EtcdEndpoints: []string{"localhost:2379"},
	DialTimeout:   5 * time.Second,
	MaxMsgSize:    4 << 20, // 4MB
}

// ServerOption 定义选项函数类型
type ServerOption func(*ServerOptions)

// WithEtcdEndpoints 设置etcd端点
func WithEtcdEndpoints(endpoints []string) ServerOption {
	return func(o *ServerOptions) {
		o.EtcdEndpoints = endpoints
	}
}

// WithDialTimeout 设置连接超时
func WithDialTimeout(timeout time.Duration) ServerOption {
	return func(o *ServerOptions) {
		o.DialTimeout = timeout
	}
}

// WithTLS 设置TLS配置
func WithTLS(certFile, keyFile string) ServerOption {
	return func(o *ServerOptions) {
		o.TLS = true
		o.CertFile = certFile
		o.KeyFile = keyFile
	}
}

// NewServer 创建一个新的缓存服务器实例。
//
// 参数：
//   - addr: 监听地址，格式为 "host:port"，如 ":8001" 或 "0.0.0.0:8001"
//   - svcName: 服务名称，用于 etcd 注册和集群标识
//   - opts: 可选的配置选项，如 WithEtcdEndpoints、WithTLS 等
//
// 返回值：
//   - *Server: 创建的服务器实例
//   - error: 创建过程中的错误，如 etcd 连接失败、TLS 证书加载失败等
//
// 创建流程：
//  1. 解析并应用配置选项
//  2. 创建 etcd 客户端连接
//  3. 创建 gRPC 服务器（支持 TLS）
//  4. 注册缓存服务和健康检查服务
//
// 示例：
//
//	srv, err := NewServer(":8001", "my-cache",
//	    WithEtcdEndpoints([]string{"etcd1:2379", "etcd2:2379"}),
//	    WithTLS("cert.pem", "key.pem"),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
func NewServer(addr, svcName string, opts ...ServerOption) (*Server, error) {
	// 从默认配置开始，应用用户传入的选项函数
	// 这种 Functional Options 模式允许用户只设置需要的选项，其余使用默认值
	options := DefaultServerOptions
	for _, opt := range opts {
		opt(options)
	}

	// 创建 etcd 客户端，用于服务注册和发现
	// Endpoints: etcd 集群的节点地址列表
	// DialTimeout: 连接超时时间，防止无限等待
	etcdCli, err := clientv3.New(clientv3.Config{
		Endpoints:   options.EtcdEndpoints,
		DialTimeout: options.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %v", err)
	}

	// 配置 gRPC 服务器选项
	var serverOpts []grpc.ServerOption
	// 设置最大接收消息大小，防止缓存值过大导致请求失败
	// 默认值 4MB，可通过 WithMaxMsgSize 选项调整
	serverOpts = append(serverOpts, grpc.MaxRecvMsgSize(options.MaxMsgSize))

	// 如果启用 TLS，加载证书并配置加密传输
	// TLS 配置确保节点间通信的安全性，防止数据被窃听或篡改
	if options.TLS {
		creds, err := loadTLSCredentials(options.CertFile, options.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS credentials: %v", err)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}

	// 创建 Server 实例，初始化所有字段
	// addr 和 svcName 用于服务注册，groups 使用 sync.Map 保证并发安全
	srv := &Server{
		addr:       addr,
		svcName:    svcName,
		groups:     &sync.Map{},
		grpcServer: grpc.NewServer(serverOpts...),
		etcdCli:    etcdCli,
		stopCh:     make(chan error),
		opts:       options,
	}

	// 将 Server 实例注册为 gRPC 服务的实现
	// 这样其他节点可以通过 gRPC 调用 Get、Set、Delete 方法
	pb.RegisterCacheServiceServer(srv.grpcServer, srv)

	// 注册 gRPC 健康检查服务
	// 健康检查用于负载均衡器或服务发现组件检测节点是否可用
	// 当节点不健康时，可以从服务发现中剔除，避免流量路由到故障节点
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(srv.grpcServer, healthServer)
	// 设置服务状态为 SERVING，表示节点已准备好接收请求
	healthServer.SetServingStatus(svcName, healthpb.HealthCheckResponse_SERVING)

	return srv, nil
}

// Start 启动服务器
func (s *Server) Start() error {
	// 启动gRPC服务器
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	// 注册到etcd
	stopCh := make(chan error)
	go func() {
		if err := registry.Register(s.svcName, s.addr, stopCh); err != nil {
			log.Printf("[Server] ERROR: failed to register service: %v", err)
			close(stopCh)
			return
		}
	}()

	log.Printf("[Server] starting at %s", s.addr)
	return s.grpcServer.Serve(lis)
}

// Stop 停止服务器
func (s *Server) Stop() {
	close(s.stopCh)
	s.grpcServer.GracefulStop()
	if s.etcdCli != nil {
		s.etcdCli.Close()
	}
}

// Get 实现Cache服务的Get方法
func (s *Server) Get(ctx context.Context, req *pb.Request) (*pb.ResponseForGet, error) {
	group := GetGroup(req.Group)
	if group == nil {
		return nil, fmt.Errorf("group %s not found", req.Group)
	}

	view, err := group.Get(ctx, req.Key)
	if err != nil {
		return nil, err
	}

	return &pb.ResponseForGet{Value: view.ByteSLice()}, nil
}

// Set 实现Cache服务的Set方法
func (s *Server) Set(ctx context.Context, req *pb.Request) (*pb.ResponseForGet, error) {
	group := GetGroup(req.Group)
	if group == nil {
		return nil, fmt.Errorf("group %s not found", req.Group)
	}

	// 从 context 中获取标记，如果没有则创建新的 context
	fromPeer := ctx.Value("from_peer")
	if fromPeer == nil {
		ctx = context.WithValue(ctx, "from_peer", true)
	}

	if err := group.Set(ctx, req.Key, req.Value); err != nil {
		return nil, err
	}

	return &pb.ResponseForGet{Value: req.Value}, nil
}

// Delete 实现Cache服务的Delete方法
func (s *Server) Delete(ctx context.Context, req *pb.Request) (*pb.ResponseForDelete, error) {
	group := GetGroup(req.Group)
	if group == nil {
		return nil, fmt.Errorf("group %s not found", req.Group)
	}

	err := group.Delete(ctx, req.Key)
	return &pb.ResponseForDelete{Value: err == nil}, err
}

// loadTLSCredentials 加载TLS证书
func loadTLSCredentials(certFile, keyFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
	}), nil
}
