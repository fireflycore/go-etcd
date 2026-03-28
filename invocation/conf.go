package invocation

import (
	"time"

	microInvocation "github.com/fireflycore/go-micro/invocation"
)

const (
	// DefaultNamespace 是 etcd 轻量实现下的默认命名空间。
	DefaultNamespace = "default"
	// DefaultCacheTTL 是本地 endpoint 缓存的默认有效期。
	DefaultCacheTTL = 5 * time.Second
)

// Conf 定义 etcd invocation 轻量实现的配置。
//
// 该配置不再表达旧 registry 模型中的节点注册语义，
// 而是围绕“如何把 ServiceRef 解析成 Target”组织。
type Conf struct {
	// Namespace 是默认命名空间。
	// 当 ServiceRef 未显式提供 namespace 时，可回退到该值。
	Namespace string `json:"namespace"`
	// DefaultPort 是默认 gRPC 端口。
	DefaultPort uint16 `json:"default_port"`
	// ClusterDomain 是 service DNS 所使用的集群域。
	ClusterDomain string `json:"cluster_domain"`
	// ResolverScheme 是最终生成 gRPC target 时使用的 resolver scheme。
	ResolverScheme string `json:"resolver_scheme"`
	// PreferServiceDNS 表示优先直接返回 service 级 DNS 目标。
	//
	// 适用场景：
	// - 已通过 CoreDNS 或其他机制提供稳定服务名；
	// - 期望让 etcd 轻量实现更接近 K8s + Istio 的调用体验。
	//
	// 若为 false，则 Locator 会从 etcd 中读取实例列表，并在本地做轻量 endpoint 选择。
	PreferServiceDNS bool `json:"prefer_service_dns"`
	// CacheTTL 表示 endpoint 列表缓存有效期。
	CacheTTL time.Duration `json:"cache_ttl"`
}

// Bootstrap 补齐配置默认值。
func (c *Conf) Bootstrap() {
	if c.Namespace == "" {
		c.Namespace = DefaultNamespace
	}
	if c.CacheTTL <= 0 {
		c.CacheTTL = DefaultCacheTTL
	}
	if c.ClusterDomain == "" {
		c.ClusterDomain = microInvocation.DefaultClusterDomain
	}
	if c.ResolverScheme == "" {
		c.ResolverScheme = microInvocation.DefaultResolverScheme
	}
}
