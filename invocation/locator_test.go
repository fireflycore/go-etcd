package invocation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	microInvocation "github.com/fireflycore/go-micro/invocation"
	microRegistry "github.com/fireflycore/go-micro/registry"
	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type fakeKVGetter struct {
	response *clientv3.GetResponse
	err      error
}

func (f fakeKVGetter) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	return f.response, f.err
}

func TestLocatorResolvePreferServiceDNS(t *testing.T) {
	locator := &Locator{
		client: fakeKVGetter{},
		conf: Conf{
			Namespace:        "default",
			DefaultPort:      9000,
			ClusterDomain:    microInvocation.DefaultClusterDomain,
			ResolverScheme:   microInvocation.DefaultResolverScheme,
			PreferServiceDNS: true,
			CacheTTL:         DefaultCacheTTL,
		},
		cache:   map[string]cachedEndpoints{},
		cursor:  map[string]uint64{},
		nowFunc: func() time.Time { return time.Unix(0, 0) },
	}

	target, err := locator.Resolve(context.Background(), microInvocation.ServiceRef{
		Service:   "auth",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if target.GRPCTarget() != "dns:///auth.default.svc.cluster.local:9000" {
		t.Fatalf("unexpected target: %s", target.GRPCTarget())
	}
}

func TestLocatorResolveReturnsEndpointTarget(t *testing.T) {
	raw, err := json.Marshal(&microRegistry.ServiceNode{
		Weight: 100,
		Meta: &microRegistry.ServiceMeta{
			AppId:      "auth",
			InstanceId: "i-1",
			Env:        "dev",
		},
		Network: &microRegistry.Network{
			Internal: "127.0.0.1:9000",
		},
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	locator := &Locator{
		client: fakeKVGetter{
			response: &clientv3.GetResponse{
				Kvs: []*mvccpb.KeyValue{
					{Value: raw},
				},
			},
		},
		conf: Conf{
			Namespace: "default",
			CacheTTL:  DefaultCacheTTL,
		},
		cache:   map[string]cachedEndpoints{},
		cursor:  map[string]uint64{},
		nowFunc: time.Now,
	}

	target, err := locator.Resolve(context.Background(), microInvocation.ServiceRef{
		Service:   "auth",
		Namespace: "default",
		Env:       "dev",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if target.Host != "127.0.0.1" {
		t.Fatalf("unexpected host: %s", target.Host)
	}
	if target.Port != 9000 {
		t.Fatalf("unexpected port: %d", target.Port)
	}
}
