package invocation

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	microInvocation "github.com/fireflycore/go-micro/invocation"
	microRegistry "github.com/fireflycore/go-micro/registry"
	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// kvGetter 抽象 etcd 的 Get 能力，便于在单元测试中注入假实现。
type kvGetter interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

type cachedEndpoints struct {
	endpoints []microInvocation.ServiceEndpoint
	expiresAt time.Time
}

// Locator 是基于 etcd 的 invocation 定位器。
//
// 它支持两种模式：
// 1. PreferServiceDNS=true：直接返回 service 级 DNS 目标；
// 2. PreferServiceDNS=false：读取 etcd 中已注册的实例列表，在本地做轻量 endpoint 选择。
//
// 这样既能兼容“类 service mesh”的统一调用语义，
// 也能在没有稳定 DNS 的场景下直接工作。
type Locator struct {
	client kvGetter
	conf   Conf

	mu      sync.Mutex
	cache   map[string]cachedEndpoints
	cursor  map[string]uint64
	nowFunc func() time.Time
}

// NewLocator 创建 etcd invocation 定位器。
func NewLocator(client *clientv3.Client, conf *Conf) (*Locator, error) {
	if client == nil {
		return nil, fmt.Errorf(microRegistry.ErrClientIsNilFormat, "etcd")
	}
	if conf == nil {
		conf = &Conf{}
	}
	conf.Bootstrap()

	return &Locator{
		client:  client,
		conf:    *conf,
		cache:   make(map[string]cachedEndpoints),
		cursor:  make(map[string]uint64),
		nowFunc: time.Now,
	}, nil
}

// Resolve 根据 ServiceRef 解析最终 Target。
func (l *Locator) Resolve(ctx context.Context, ref microInvocation.ServiceRef) (microInvocation.Target, error) {
	ref = l.normalizeRef(ref)

	if l.conf.PreferServiceDNS {
		return microInvocation.BuildTarget(ref, microInvocation.TargetOptions{
			DefaultPort:    l.conf.DefaultPort,
			ClusterDomain:  l.conf.ClusterDomain,
			ResolverScheme: l.conf.ResolverScheme,
		})
	}

	endpoints, err := l.loadEndpoints(ctx, ref)
	if err != nil {
		return microInvocation.Target{}, err
	}

	endpoint, err := l.pickEndpoint(ref, endpoints)
	if err != nil {
		return microInvocation.Target{}, err
	}

	host, port, err := splitAddress(endpoint.Address)
	if err != nil {
		return microInvocation.Target{}, err
	}

	return microInvocation.Target{
		Host: host,
		Port: port,
	}, nil
}

// normalizeRef 为 ServiceRef 填充 etcd 轻量实现所需的默认值。
func (l *Locator) normalizeRef(ref microInvocation.ServiceRef) microInvocation.ServiceRef {
	if strings.TrimSpace(ref.Namespace) == "" {
		ref.Namespace = l.conf.Namespace
	}
	return ref
}

func (l *Locator) loadEndpoints(ctx context.Context, ref microInvocation.ServiceRef) ([]microInvocation.ServiceEndpoint, error) {
	key := l.cacheKey(ref)

	l.mu.Lock()
	if item, ok := l.cache[key]; ok && l.nowFunc().Before(item.expiresAt) {
		out := append([]microInvocation.ServiceEndpoint(nil), item.endpoints...)
		l.mu.Unlock()
		return out, nil
	}
	l.mu.Unlock()

	endpoints, err := l.fetchEndpoints(ctx, ref)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	l.cache[key] = cachedEndpoints{
		endpoints: append([]microInvocation.ServiceEndpoint(nil), endpoints...),
		expiresAt: l.nowFunc().Add(l.conf.CacheTTL),
	}
	l.mu.Unlock()

	return endpoints, nil
}

func (l *Locator) fetchEndpoints(ctx context.Context, ref microInvocation.ServiceRef) ([]microInvocation.ServiceEndpoint, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}

	prefix := fmt.Sprintf("%s/%s/%s", ref.NamespaceName(), strings.TrimSpace(ref.Env), ref.ServiceName())
	response, err := l.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	endpoints := make([]microInvocation.ServiceEndpoint, 0, len(response.Kvs))
	for _, kv := range response.Kvs {
		endpoint, ok := decodeEndpoint(kv)
		if !ok {
			continue
		}
		endpoints = append(endpoints, endpoint)
	}
	if len(endpoints) == 0 {
		return nil, microInvocation.ErrTargetHostEmpty
	}

	return endpoints, nil
}

func (l *Locator) pickEndpoint(ref microInvocation.ServiceRef, endpoints []microInvocation.ServiceEndpoint) (microInvocation.ServiceEndpoint, error) {
	if len(endpoints) == 0 {
		return microInvocation.ServiceEndpoint{}, microInvocation.ErrTargetHostEmpty
	}

	key := l.cacheKey(ref)

	l.mu.Lock()
	defer l.mu.Unlock()

	index := l.cursor[key] % uint64(len(endpoints))
	l.cursor[key]++
	return endpoints[index], nil
}

func (l *Locator) cacheKey(ref microInvocation.ServiceRef) string {
	return strings.Join([]string{ref.ServiceName(), ref.NamespaceName(), strings.TrimSpace(ref.Env)}, "|")
}

func decodeEndpoint(kv *mvccpb.KeyValue) (microInvocation.ServiceEndpoint, bool) {
	if kv == nil || len(kv.Value) == 0 {
		return microInvocation.ServiceEndpoint{}, false
	}

	var node microRegistry.ServiceNode
	if err := json.Unmarshal(kv.Value, &node); err != nil {
		return microInvocation.ServiceEndpoint{}, false
	}
	if node.Network == nil || strings.TrimSpace(node.Network.Internal) == "" {
		return microInvocation.ServiceEndpoint{}, false
	}

	meta := map[string]string{}
	if node.Meta != nil {
		meta["app_id"] = node.Meta.AppId
		meta["instance_id"] = node.Meta.InstanceId
		meta["env"] = node.Meta.Env
		meta["version"] = node.Meta.Version
	}

	return microInvocation.ServiceEndpoint{
		Address: node.Network.Internal,
		Weight:  node.Weight,
		Healthy: true,
		Meta:    meta,
	}, true
}

func splitAddress(raw string) (string, uint16, error) {
	host, portRaw, err := net.SplitHostPort(raw)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return "", 0, err
	}
	return host, uint16(port), nil
}
