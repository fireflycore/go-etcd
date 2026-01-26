# Registry

`registry` 子包提供基于 etcd v3 的服务注册与发现实现，接口类型复用 `github.com/fireflycore/go-micro/registry`。

## 服务注册

```go
package main

import (
	"log"

	etcd "github.com/fireflycore/go-etcd/registry"
	"github.com/fireflycore/go-micro/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func main() {
	cli, err := clientv3.New(clientv3.Config{Endpoints: []string{"127.0.0.1:2379"}})
	if err != nil {
		log.Fatal(err)
	}
	defer cli.Close()

	reg, err := etcd.NewRegister(cli, &registry.Meta{Env: "prod", AppId: "demo", Version: "v0.0.1"}, &registry.ServiceConf{})
	if err != nil {
		log.Fatal(err)
	}
	defer reg.Uninstall()

	go reg.SustainLease()
}
```

## 服务发现

```go
package main

import (
	"log"

	etcd "github.com/fireflycore/go-etcd/registry"
	"github.com/fireflycore/go-micro/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func main() {
	cli, err := clientv3.New(clientv3.Config{Endpoints: []string{"127.0.0.1:2379"}})
	if err != nil {
		log.Fatal(err)
	}
	defer cli.Close()

	disc, err := etcd.NewDiscover(cli, &registry.Meta{Env: "prod"}, &registry.ServiceConf{})
	if err != nil {
		log.Fatal(err)
	}

	go disc.Watcher()
	defer disc.Unwatch()

	_, _ = disc.GetService("/package.Service/Method")
}
```
