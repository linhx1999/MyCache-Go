# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

KamaCache-Go 是一个用 Go 实现的分布式缓存系统，类似于 Redis 的核心功能。这是一个教育性质的分布式缓存项目，涵盖了缓存系统的核心概念和分布式架构设计。

### 核心特性
- 分布式缓存支持（基于 gRPC 通信）
- 多种缓存淘汰策略（LRU、LRU2 两级缓存）
- 一致性哈希算法（支持虚拟节点动态调整）
- 服务发现与注册（基于 etcd）
- SingleFlight 防止缓存击穿
- 高并发优化（原子操作、读写锁、分桶设计）

## 构建和测试

### 基本命令

```bash
# 构建项目
go build

# 运行所有测试
go test ./...

# 运行特定包的测试
go test ./store

# 运行特定测试函数
go test ./store -run TestCacheBasic

# 运行基准测试
go test ./store -bench=.

# 运行示例（需要先启动 etcd）
docker-compose up -d  # 启动 etcd
go run example/test.go -port 8001 -node A
go run example/test.go -port 8002 -node B
go run example/test.go -port 8003 -node C
```

### Protocol Buffers 生成

如果修改了 `pb/kama.proto`，需要重新生成代码：

```bash
protoc --go_out=. --go-grpc_out=. pb/kama.proto
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
- 缓存的命名空间管理，支持多个独立的 Group
- 集成 SingleFlight 防止缓存击穿
- 支持分布式节点选择（PeerPicker）
- 统计信息跟踪（命中率、加载耗时等）

**关键流程**：
- `Get(key)` → 检查本地缓存 → 未命中则从远程节点获取 → 仍未命中则调用 Getter 回调加载数据

#### 2. **Cache（缓存封装）** - `cache.go`
- 封装底层存储实现，提供统一接口
- 延迟初始化（双重检查锁定）
- 原子操作管理状态（initialized、closed）
- 统计命中/未命中次数

#### 3. **Store（存储引擎）** - `store/` 包
- **Store 接口**：统一的存储规范
- **LRU**：标准 LRU 算法实现（`store/lru.go`）
- **LRU2**：两级缓存优化（`store/lru2.go`）
  - 一级缓存：热点数据，小容量快速访问
  - 二级缓存：温数据，大容量存储
  - 分桶设计：减少锁竞争，提高并发性能

#### 4. **一致性哈希** - `consistenthash/` 包
- 虚拟节点支持，数据均匀分布
- 动态负载均衡（当节点负载不均衡度超过 25% 时自动调整）
- 基于权重的节点选择

#### 5. **服务注册与发现** - `registry/register.go`
- 基于 etcd 的服务注册
- 租约机制（自动续期）
- 服务注销（优雅关闭）

#### 6. **SingleFlight** - `singleflight/singleflight.go`
- 防止缓存击穿
- 确保相同 key 的并发请求只执行一次加载
- 使用 sync.Map 优化并发性能

### 并发控制策略

1. **原子操作**：使用 `sync/atomic` 管理状态标志（initialized、closed）
2. **读写锁**：Cache 和 Group 使用 `sync.RWMutex` 保护共享数据
3. **分桶设计**：LRU2 使用多个桶减少锁竞争
4. **双重检查锁定**：延迟初始化优化性能
5. **SingleFlight**：防止重复加载

## 配置和选项

### Cache 选项（CacheOptions）
- `CacheType`: LRU 或 LRU2
- `MaxBytes`: 最大内存使用量
- `BucketCount`: 缓存桶数量（LRU2）
- `CapPerBucket`: 每个桶的容量（LRU2）
- `Level2Cap`: 二级缓存容量（LRU2）
- `CleanupTime`: 过期清理间隔
- `OnEvicted`: 淘汰回调函数

### Server 选项
- `WithEtcdEndpoints`: etcd 地址
- `WithDialTimeout`: gRPC 连接超时
- `WithTLS`: TLS 配置

### Group 选项
- `WithExpiration`: 缓存过期时间
- `WithPeers`: 分布式节点选择器
- `WithCacheOptions`: 缓存配置

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

1. **接口模式**：Store、Getter、PeerPicker 接口便于扩展
2. **选项模式**：使用函数式选项（Functional Options）配置组件
3. **延迟初始化**：Cache 使用双重检查锁定延迟创建 Store
4. **命名空间隔离**：Group 提供多个独立的缓存空间
5. **回调机制**：OnEvicted、Getter 回调提供扩展点

## 注意事项

1. **并发安全**：所有公共方法都是并发安全的
2. **资源清理**：使用 defer 确保资源释放，调用 Close() 方法
3. **错误处理**：检查错误返回，特别是网络操作和数据源加载
4. **测试依赖**：分布式测试需要 etcd，使用 docker-compose 启动
5. **性能优化**：避免在热路径中使用锁，优先使用原子操作

## 常见开发任务

### 添加新的缓存策略
1. 在 `store/` 包中实现新的 Store 接口
2. 在 `store/store.go` 的 `CacheType` 枚举中添加新类型
3. 在 `store/store.go` 的 `NewStore` 函数中添加创建逻辑

### 添加新的节点选择策略
1. 实现 `PeerPicker` 接口（`peers.go`）
2. 在 Group 中注册新的 Picker

### 修改 Protocol Buffers 定义
1. 编辑 `pb/kama.proto`
2. 重新生成代码：`protoc --go_out=. --go-grpc_out=. pb/kama.proto`
