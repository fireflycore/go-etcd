## go-etcd

基于 etcd clientv3 的轻量封装，当前包只提供根包能力，用于创建和管理 etcd client。

- 提供 `*clientv3.Client` 初始化入口
- 统一封装 Endpoint、认证与 TLS 参数
- 适合作为独立基础包直接使用

## 安装

```bash
go get github.com/fireflycore/go-etcd
```

## 快速开始

### 基础用法

```go
package main

import (
	"log"

	"github.com/fireflycore/go-etcd"
)

func main() {
	cli, err := etcd.New(&etcd.Conf{
		Endpoint: []string{"127.0.0.1:2379"},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer cli.Close()
}
```

### 用户名密码

当同时配置了 Username 与 Password 时，会启用用户名密码认证：

```go
cli, err := etcd.New(&etcd.Conf{
	Endpoint: []string{"127.0.0.1:2379"},
	Username: "root",
	Password: "root",
})
```

### TLS（双向证书）

当 Conf.Tls 同时配置了 CaCert / ClientCert / ClientCertKey 三个文件路径时启用 TLS，否则视为不启用：

```go
cli, err := etcd.New(&etcd.Conf{
	Endpoint: []string{"127.0.0.1:2379"},
	Tls: &etcd.TLS{
		CaCert:        "/path/to/ca.pem",
		ClientCert:    "/path/to/client.pem",
		ClientCertKey: "/path/to/client.key",
	},
})
```

## 配置说明

初始化配置为 etcd.Conf。

常用字段：
- Endpoint：etcd 节点列表（如 127.0.0.1:2379）
- Username/Password：用户名密码（同时配置时启用）
- Tls：TLS 双向证书配置（同时配置三个文件路径时启用）
