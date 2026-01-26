package registry

import (
	"os"
	"strings"
	"testing"
	"time"

	micro "github.com/fireflycore/go-micro/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestRegister(t *testing.T) {
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

	config := &micro.ServiceConf{
		Network: &micro.Network{
			SN:       "test",
			Internal: "127.0.0.1",
			External: "127.0.0.1",
		},
		Kernel:    &micro.Kernel{},
		Namespace: "test-namespace",
		TTL:       10,
		MaxRetry:  0,
	}

	meta := &micro.Meta{
		AppId:   "test-service",
		Env:     "prod",
		Version: "v0.0.1",
	}

	reg, err := NewRegister(cli, meta, config)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Uninstall()

	service := &micro.ServiceNode{
		Methods: map[string]bool{
			"/test.Service/Ping": true,
		},
	}

	if err := reg.Install(service); err != nil {
		t.Fatal(err)
	}

	go reg.SustainLease()
	time.Sleep(100 * time.Millisecond)
}
