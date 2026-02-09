# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

MyCache-Go 是一个用 Go 实现的分布式缓存系统，类似于 Redis 的核心功能。这是一个教育性质的分布式缓存项目，涵盖了缓存系统的核心概念和分布式架构设计。

### 核心特性
- **分布式缓存支持**：基于 gRPC 通信，支持多节点缓存数据同步
- **多种缓存淘汰策略**：支持 LRU 和 LRU2（两级缓存）
- **一致性哈希算法**：支持虚拟节点动态调整，自动负载均衡
- **服务发现与注册**：基于 etcd 的服务注册、发现和健康检查
- **SingleFlight 防止缓存击穿**：确保相同 key 的并发请求只执行一次加载
- **高并发优化**：原子操作、读写锁、分桶设计、延迟初始化
- **缓存过期管理**：支持 TTL 和定时清理机制

### 项目状态
项目已完成核心功能实现，并进行了多次重构优化以提高代码质量和可读性。最近的改进包括：
- 从 `Getter` 重命名为 `DataSource` 以更准确描述接口功能
- 统一使用 `atomic.Int64` 和 `atomic.Int32` 替代手动原子操作
- 优化变量命名（如 `localCache`, `singleFlightLoader`, `byteView`）
- 从 `logrus` 迁移到标准库 `log` 以减少依赖
- 统一错误消息前缀为 "cache:"
- 统一日志前缀为 "MyCache"

## 构建和测试

### 环境要求

- **Go 版本**: 1.22 或更高版本（项目使用 Go 1.22.11 toolchain）
- **etcd**: 分布式功能需要 etcd（使用 Docker Compose 启动）
- **protobuf**: 需要安装 `protoc` 编译器（如果修改 proto 文件）

### 环境准备

**重要**: 分布式功能需要 etcd，启动 etcd:

```bash
docker-compose up -d
```

### 基本命令

```bash
# 构建项目
go build ./...

# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./store -v

# 运行特定测试函数
go test ./store -run TestCacheBasic -v

# 运行基准测试
go test ./store -bench=. -benchmem

# 运行多节点分布式示例（需要在不同终端运行）
go run example/test.go -port 8001 -node A
go run example/test.go -port 8002 -node B
go run example/test.go -port 8003 -node C
```

### Protocol Buffers 生成

如果修改了 `pb/kama.proto`，需要重新生成代码：

```bash
protoc --go_out=. --go-grpc_out=. pb/kama.proto
```

### 代码格式化

项目使用标准的 Go 代码格式：

```bash
# 格式化所有代码
go fmt ./...

# 或使用 goimports（推荐）
goimports -w .

# 运行 gofmt 检查
gofmt -l .
```

## 架构概览

### 核心层次结构

```
应用层 (example/test.go)
    ↓
Group (缓存组，命名空间管理)
    ↓
Cache (缓存封装，统一接口)
    ↓
Store (存储引擎：LRU/LRU2)
    ↓
PeerPicker (节点选择器，一致性哈希)
    ↓
gRPC Client/Server (节点间通信)
```

### 关键模块说明

#### 1. **Group（缓存组）** - `group.go`
缓存的命名空间管理层，支持多个独立的 Group。

**核心职责**：
- 缓存的命名空间管理，支持多个独立的 Group
- 集成 SingleFlight 防止缓存击穿
- 支持分布式节点选择（PeerPicker）
- 统计信息跟踪（命中率、加载耗时等）

**关键流程**：
```
Get(key) → 本地缓存命中 → 返回数据
         ↓ 未命中
    使用 SingleFlight 加载（防止重复请求）
         ↓
    尝试从远程节点获取（PeerPicker）
         ↓ 未命中
    调用 DataSource 回调从数据源加载
         ↓
    存入本地缓存并返回
```

**Set/Delete 同步机制**：
- 本地操作完成后，异步同步到其他节点（使用 `from_peer` 标记避免循环同步）
- 支持带过期时间的缓存设置

#### 2. **Cache（缓存封装）** - `cache.go`
封装底层存储实现，提供统一接口。

**核心特性**：
- 延迟初始化（双重检查锁定）
- 原子操作管理状态（initialized、closed）
- 统计命中/未命中次数
- 线程安全的 Add、Get、Delete 操作

**初始化流程**：
```go
ensureInitialized() → 快速检查（原子操作） → 双重检查锁定 → 创建 Store
```

#### 3. **Store（存储引擎）** - `store/` 包
底层存储实现，支持多种淘汰策略。

**Store 接口**：统一的存储规范
- `Get/Set/SetWithExpiration/Delete/Clear/Len/Close`

**LRU 实现** (`store/lru.go`)：
- 标准 LRU 算法
- 支持最大字节数限制
- 支持过期时间

**LRU2 实现** (`store/lru2.go`)：
- **一级缓存**：热点数据，小容量快速访问
- **二级缓存**：温数据，大容量存储
- **分桶设计**：减少锁竞争，提高并发性能（默认 16 个桶）
- **数据流动**：一级缓存未命中 → 检查二级缓存 → 升级到一级缓存

#### 4. **一致性哈希** - `consistenthash/` 包
实现一致性哈希算法，支持节点动态增减。

**核心特性**：
- 虚拟节点支持（默认每个节点 50 个虚拟节点）
- 动态负载均衡（当负载不均衡度超过 25% 时自动调整虚拟节点数）
- 虚拟节点数范围：10-200
- 基于权重的节点选择
- CRC32 哈希函数

**负载均衡机制**：
```
每秒检查一次负载分布
当最大负载偏差 > 25%
→ 调整各节点的虚拟节点数量
→ 高负载节点减少虚拟节点
→ 低负载节点增加虚拟节点
```

#### 5. **服务注册与发现** - `peers.go` + `registry/register.go`

**ClientPicker** (`peers.go`)：
- 基于 etcd 的服务发现
- 监听服务变化（全量更新 + 增量 Watch）
- 维护到其他节点的 gRPC 客户端连接池
- 集成一致性哈希进行节点选择

**服务注册** (`registry/register.go`)：
- 基于 etcd 的服务注册（key: `/services/{svcName}/{addr}`）
- 租约机制（10 秒租约，自动续期）
- 服务注销（优雅关闭时撤销租约）
- 自动获取本地 IP 地址

#### 6. **SingleFlight** - `singleflight/singleflight.go`
防止缓存击穿的并发控制机制。

**实现特性**：
- 使用 `sync.Map` 优化并发性能
- 确保相同 key 的并发请求只执行一次加载
- 请求完成后自动清理，避免内存泄漏
- 所有等待的请求共享同一个结果

#### 7. **Server/Client** - `server.go` + `client.go`
gRPC 服务端和客户端实现。

**Server** (`server.go`)：
- 实现 gRPC 服务：Get、Set、Delete
- 支持健康检查（grpc_health_v1）
- 支持 TLS 加密通信
- 优雅关闭机制

**Client** (`client.go`)：
- 封装 gRPC 客户端连接
- 实现 Peer 接口供 ClientPicker 调用
- 自动重连和错误处理

### 并发控制策略

1. **原子操作**：使用 `sync/atomic` 管理状态标志（initialized、closed、统计计数器）
2. **读写锁**：Cache 和 Group 使用 `sync.RWMutex` 保护共享数据
3. **分桶锁**：LRU2 使用多个桶（默认 16 个）减少锁竞争，每个桶独立锁
4. **双重检查锁定**：Cache 延迟初始化优化性能
5. **SingleFlight**：防止重复加载，使用 `sync.Map` 管理并发请求
6. **sync.Map**：Group 使用全局 map 存储多个 Group 实例，使用读写锁保护

## 配置和选项

### Cache 选项（CacheOptions）
- `CacheType`: LRU 或 LRU2（默认 LRU2）
- `MaxBytes`: 最大内存使用量（默认 8MB）
- `BucketCount`: 缓存桶数量（LRU2，默认 16）
- `CapPerBucket`: 每个桶的容量（LRU2，默认 512）
- `Level2Cap`: 二级缓存桶的容量（LRU2，默认 256）
- `CleanupTime`: 过期清理间隔（默认 1 分钟）
- `OnEvicted`: 淘汰回调函数

### Server 选项（ServerOption）
- `WithEtcdEndpoints`: etcd 地址（默认 localhost:2379）
- `WithDialTimeout`: gRPC 连接超时（默认 5 秒）
- `WithTLS`: TLS 配置（指定证书文件和密钥文件）

### Group 选项（GroupOption）
- `WithExpiration`: 缓存过期时间（0 表示永不过期）
- `WithPeers`: 分布式节点选择器（PeerPicker）
- `WithCacheOptions`: 缓存配置（CacheOptions）

### 一致性哈希配置（Config）
- `DefaultReplicas`: 每个节点的虚拟节点数（默认 50）
- `MinReplicas`: 最小虚拟节点数（默认 10）
- `MaxReplicas`: 最大虚拟节点数（默认 200）
- `HashFunc`: 哈希函数（默认 CRC32）
- `LoadBalanceThreshold`: 负载均衡阈值（默认 0.25，即 25%）

## 测试要点

### 单元测试
- `store/lru2_test.go`：完整的缓存操作测试
  - 基本操作（Get、Set、Delete）
  - LRU 淘汰策略
  - 过期时间
  - 并发安全性
  - 统计信息

### 集成测试
- `example/test.go`：多节点分布式测试
  - 需要先启动 etcd（`docker-compose up -d`）
  - 支持命令行参数指定端口和节点 ID

## 关键设计模式

1. **接口模式**：Store、DataSource、PeerPicker 接口便于扩展
2. **选项模式**：使用函数式选项（Functional Options）配置组件
3. **延迟初始化**：Cache 使用双重检查锁定延迟创建 Store
4. **命名空间隔离**：Group 提供多个独立的缓存空间
5. **回调机制**：OnEvicted、DataSource 回调提供扩展点

## 重要注意事项

1. **并发安全**：所有公共方法都是并发安全的，内部使用了适当的锁机制
2. **资源清理**：
   - 使用 defer 确保资源释放
   - 调用 `Close()` 方法优雅关闭组件
   - Group 关闭后会拒绝所有操作（返回 `ErrGroupClosed`）
3. **错误处理**：
   - 检查错误返回，特别是网络操作和数据源加载
   - 空键返回 `ErrKeyRequired`
   - 空值返回 `ErrValueRequired`
4. **测试依赖**：分布式测试需要 etcd，使用 `docker-compose up -d` 启动
5. **性能优化**：
   - 避免在热路径中使用锁，优先使用原子操作
   - Cache 使用延迟初始化，减少启动开销
   - LRU2 使用分桶设计，减少锁竞争
6. **数据同步**：
   - Set/Delete 操作会异步同步到其他节点
   - 使用 `from_peer` 上下文标记避免循环同步
   - 远程节点获取失败会降级到本地数据源加载

## 常见开发任务

### 添加新的缓存淘汰策略
1. 在 `store/` 包中实现 Store 接口（参考 `lru.go` 或 `lru2.go`）
2. 在 `store/types.go` 的 `CacheType` 枚举中添加新类型
3. 在 `store/types.go` 的 `NewStore` 函数中添加创建逻辑
4. 编写单元测试验证功能正确性

### 添加新的节点选择策略
1. 实现 `PeerPicker` 接口（`peers.go`）
   - `PickPeer(key string) (Peer, bool, bool)`
   - `Close() error`
2. 在 Group 创建时使用 `WithPeers` 选项注册新的 Picker

### 修改 Protocol Buffers 定义
1. 编辑 `pb/kama.proto`
2. 重新生成 Go 代码：
   ```bash
   protoc --go_out=. --go-grpc_out=. pb/kama.proto
   ```
3. 更新 `server.go` 和 `client.go` 中的实现

### 调试分布式问题
1. 检查 etcd 中注册的服务：
   ```bash
   etcdctl get /services/my-cache --prefix
   ```
2. 查看节点日志，确认服务发现和连接状态
3. 使用 `picker.PrintPeers()` 打印当前发现的节点（仅用于调试）
4. 检查防火墙和网络连通性

### 性能调优
1. **调整缓存大小**：根据实际数据量调整 `MaxBytes`
2. **调整分桶数量**：LRU2 可增加 `BucketCount` 以减少锁竞争（通常设置为 CPU 核心数的倍数）
3. **调整虚拟节点数**：一致性哈希可根据节点数量调整虚拟节点数
4. **启用统计信息**：使用 `group.Stats()` 监控命中率和加载时间
5. **调整过期时间**：合理设置 TTL 避免内存浪费

## 项目文件结构

```
MyCache-Go/
├── pb/                      # Protocol Buffers 定义和生成代码
│   ├── kama.proto          # gRPC 服务定义
│   ├── kama.pb.go          # 生成的消息类型
│   └── kama_grpc.pb.go     # 生成的 gRPC 服务代码
├── consistenthash/          # 一致性哈希实现
│   ├── con_hash.go         # 一致性哈希核心逻辑
│   └── config.go           # 配置定义
├── registry/               # 服务注册与发现
│   └── register.go         # etcd 服务注册
├── singleflight/           # SingleFlight 并发控制
│   └── singleflight.go     # 防止缓存击穿
├── store/                  # 存储引擎实现
│   ├── types.go           # Store 接口和工厂
│   ├── lru.go             # LRU 缓存实现
│   ├── lru2.go            # LRU2 两级缓存实现
│   └── lru2_test.go       # LRU2 单元测试
├── example/                # 示例代码
│   └── test.go            # 多节点分布式测试示例
├── cache.go               # Cache 封装层
├── group.go               # Group 缓存组管理
├── peers.go               # PeerPicker 节点选择器
├── server.go              # gRPC 服务端
├── client.go              # gRPC 客户端
├── byteview.go            # 不可变字节数组封装
├── utils.go               # 工具函数
├── docker-compose.yml     # etcd 容器配置
├── go.mod                 # Go 模块定义
└── CLAUDE.md              # 本文档
```

## 代码规范说明

项目遵循以下代码规范（基于 git 提交历史）：

1. **命名规范**：
   - 使用有意义的变量名，如 `localCache` 而非 `mainCache`
   - 使用 `singleFlightLoader` 而非 `loader` 以提高可读性
   - 使用 `byteView` 而非 `view` 以明确类型

2. **错误处理**：
   - 统一错误消息前缀为 `"cache:"`
   - 定义清晰的错误类型（`ErrKeyRequired`, `ErrValueRequired`, `ErrGroupClosed`）

3. **并发控制**：
   - 优先使用 `atomic.Int64` 和 `atomic.Int32` 替代手动原子操作
   - 使用 `sync.RWMutex` 保护共享数据
   - 避免在热路径中使用锁

4. **日志管理**：
   - 使用标准库 `log` 而非第三方库（已从 logrus 迁移到标准库）
   - 统一日志前缀为 "MyCache"（而非 "KamaCache"）

5. **接口命名**：
   - 使用 `DataSource` 而非 `Getter` 以更准确描述功能
   - 保持接口简洁明了
