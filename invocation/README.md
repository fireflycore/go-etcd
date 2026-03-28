# Invocation

`go-etcd/invocation` 提供基于 etcd 的轻量服务调用实现。

它的目标不是继续暴露旧的节点发现模型，而是对齐 `go-micro/invocation` 的统一调用语义：

- 业务侧面向 `ServiceRef`
- 调用侧面向 `Target`
- 连接复用交给 `ConnectionManager`
- 底层如何从 etcd 解析服务地址，由 `Locator` 内部完成

## 包定位

`go-etcd/invocation` 是 etcd 路径下的轻量实现，适合：

- IDC 环境
- 尚未上 K8s + Istio 的场景
- 需要通过 etcd 维护服务实例信息，但对外仍保持 `service -> service` 调用体验

## 当前提供的能力

### Conf

`Conf` 用于描述 etcd invocation 的公共配置，例如：

- `Namespace`
- `DefaultPort`
- `ClusterDomain`
- `ResolverScheme`
- `PreferServiceDNS`
- `CacheTTL`

其中：

- `PreferServiceDNS=true` 时，更偏向“类 service mesh”模式
- `PreferServiceDNS=false` 时，会从 etcd 中读取实例列表，在本地做轻量 endpoint 选择

### Locator

`Locator` 是核心实现，负责把 `ServiceRef` 解析成最终 `Target`。

支持两种模式：

#### 1. Service DNS 模式

直接返回类似：

```text
dns:///auth.default.svc.cluster.local:9000
```

适合：

- 已通过 CoreDNS 或其他机制提供稳定服务名
- 希望 etcd 路径尽量对齐 K8s + Istio 的调用体验

#### 2. Endpoint 模式

从 etcd 中读取实例注册信息，解析出 `ServiceEndpoint` 列表，并在本地做轻量选择。

适合：

- 还没有稳定 service DNS
- 仍需要 etcd 直接提供底层实例地址

### NewConnectionManager

该辅助函数会把：

- etcd `Locator`
- `go-micro/invocation.ConnectionManager`

组合起来，让上层更容易直接接入统一调用模型。

## 设计说明

- etcd 只作为实现后端
- lease / watch / key 结构等细节不暴露给业务侧
- 对外语义始终与 `go-micro/invocation` 对齐
- OTel 观测链路默认继续依赖 `go-micro/invocation.ConnectionManager`

## 当前进度

当前已经完成：

- `Conf`
- `Locator`
- `NewConnectionManager`
- 单元测试

当前尚未做的内容：

- 单独的 etcd invocation README 示例代码扩展
- 更复杂的 endpoint 选择策略
- 更深的 Authz 接入封装

## 测试

当前包已通过：

- `go test ./...`
- `go vet ./...`
