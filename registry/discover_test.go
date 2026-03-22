package registry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	micro "github.com/fireflycore/go-micro/registry"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestDiscoverAdapterPutUsesKv(t *testing.T) {
	ins := &DiscoverInstance{
		meta:    &micro.Meta{Env: "prod"},
		conf:    &ServiceConf{Namespace: "test"},
		method:  make(micro.ServiceMethod),
		service: make(micro.ServiceDiscover),
	}

	oldNode := &micro.ServiceNode{
		Meta:    &micro.Meta{Env: "prod", AppId: "svc"},
		Methods: map[string]bool{"/svc.Svc/Ping": true},
		Network: &micro.Network{Internal: "10.0.0.1:8080", External: "svc.example.com:80"},
		RunDate: "2026-01-01 00:00:00",
	}
	newNode := &micro.ServiceNode{
		Meta:    &micro.Meta{Env: "prod", AppId: "svc"},
		Methods: map[string]bool{"/svc.Svc/Ping": true},
		Network: &micro.Network{Internal: "10.0.0.1:8080", External: "svc.example.com:80"},
		RunDate: "2026-01-02 00:00:00",
	}
	oldVal, err := json.Marshal(oldNode)
	if err != nil {
		t.Fatal(err)
	}
	newVal, err := json.Marshal(newNode)
	if err != nil {
		t.Fatal(err)
	}

	ins.adapter(&clientv3.Event{
		Type:   clientv3.EventTypePut,
		Kv:     &mvccpb.KeyValue{Value: newVal},
		PrevKv: &mvccpb.KeyValue{Value: oldVal},
	})

	nodes, ok := ins.service["svc"]
	if !ok || len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %v", nodes)
	}
	if nodes[0].RunDate != "2026-01-02 00:00:00" {
		t.Fatalf("expected latest node runDate, got %s", nodes[0].RunDate)
	}
	if owner := ins.method["/svc.Svc/Ping"]; owner != "svc" {
		t.Fatalf("expected method owner=svc, got %q", owner)
	}

	ins.adapter(&clientv3.Event{
		Type:   clientv3.EventTypeDelete,
		Kv:     &mvccpb.KeyValue{Value: []byte("ignored")},
		PrevKv: &mvccpb.KeyValue{Value: newVal},
	})

	if _, ok := ins.service["svc"]; ok {
		t.Fatalf("expected service removed")
	}
	if _, ok := ins.method["/svc.Svc/Ping"]; ok {
		t.Fatalf("expected method removed")
	}
}

func TestDiscoverMethodMapRefresh(t *testing.T) {
	ins := &DiscoverInstance{
		meta:    &micro.Meta{Env: "prod"},
		conf:    &ServiceConf{Namespace: "test"},
		method:  make(micro.ServiceMethod),
		service: make(micro.ServiceDiscover),
	}

	n1 := &micro.ServiceNode{
		Meta:    &micro.Meta{Env: "prod", AppId: "svc"},
		Methods: map[string]bool{"/svc.Svc/A": true, "/svc.Svc/B": true},
		Network: &micro.Network{Internal: "10.0.0.1:8080", External: "svc.example.com:80"},
		RunDate: "2026-01-01 00:00:00",
	}
	v1, err := json.Marshal(n1)
	if err != nil {
		t.Fatal(err)
	}
	ins.adapter(&clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv:   &mvccpb.KeyValue{Value: v1},
	})
	if ins.method["/svc.Svc/A"] != "svc" || ins.method["/svc.Svc/B"] != "svc" {
		t.Fatalf("expected methods A and B mapped to svc, got %#v", ins.method)
	}

	n1v2 := &micro.ServiceNode{
		Meta:    &micro.Meta{Env: "prod", AppId: "svc"},
		Methods: map[string]bool{"/svc.Svc/A": true},
		Network: &micro.Network{Internal: "10.0.0.1:8080", External: "svc.example.com:80"},
		RunDate: "2026-01-01 00:00:00",
	}
	v2, err := json.Marshal(n1v2)
	if err != nil {
		t.Fatal(err)
	}
	ins.adapter(&clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv:   &mvccpb.KeyValue{Value: v2},
	})
	if ins.method["/svc.Svc/A"] != "svc" {
		t.Fatalf("expected method A mapped to svc, got %#v", ins.method)
	}
	if _, ok := ins.method["/svc.Svc/B"]; ok {
		t.Fatalf("expected method B removed, got %#v", ins.method)
	}

	n2 := &micro.ServiceNode{
		Meta:    &micro.Meta{Env: "prod", AppId: "svc"},
		Methods: map[string]bool{"/svc.Svc/A": true, "/svc.Svc/C": true},
		Network: &micro.Network{Internal: "10.0.0.2:8080", External: "svc2.example.com:80"},
		RunDate: "2026-01-02 00:00:00",
	}
	v3, err := json.Marshal(n2)
	if err != nil {
		t.Fatal(err)
	}
	ins.adapter(&clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv:   &mvccpb.KeyValue{Value: v3},
	})
	if ins.method["/svc.Svc/C"] != "svc" {
		t.Fatalf("expected method C mapped to svc, got %#v", ins.method)
	}

	ins.adapter(&clientv3.Event{
		Type:   clientv3.EventTypeDelete,
		PrevKv: &mvccpb.KeyValue{Value: v3},
	})
	if _, ok := ins.method["/svc.Svc/C"]; ok {
		t.Fatalf("expected method C removed after node delete, got %#v", ins.method)
	}
	if ins.method["/svc.Svc/A"] != "svc" {
		t.Fatalf("expected method A still mapped to svc, got %#v", ins.method)
	}
}

func TestDiscover(t *testing.T) {
	endpointsEnv := os.Getenv("ETCD_ENDPOINTS")
	if endpointsEnv == "" {
		t.Skip("ETCD_ENDPOINTS is empty")
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints: strings.Split(endpointsEnv, ","),
		Username:  os.Getenv("ETCD_USERNAME"),
		Password:  os.Getenv("ETCD_PASSWORD"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	config := &ServiceConf{
		Network: &micro.Network{
			SN:       "test",
			Internal: "127.0.0.1",
			External: "127.0.0.1",
		},
		Kernel:    &micro.Kernel{},
		Namespace: "test-namespace",
		TTL:       10,
		MaxRetry:  3,
	}

	discovery, err := NewDiscover(cli, &micro.Meta{
		AppId:   "test-service",
		Env:     "prod",
		Version: "v0.0.1",
	}, config)
	if err != nil {
		t.Fatal(err)
	}

	dis, ok := discovery.(*DiscoverInstance)
	if !ok {
		t.Fatal("expected *DiscoverInstance")
	}

	go dis.Watcher()

	time.Sleep(100 * time.Millisecond)
	dis.Unwatch()
}
