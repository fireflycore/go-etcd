package etcd

import "github.com/fireflycore/go-utils/tlsx"

// Conf 定义 etcd 客户端初始化配置。
type Conf struct {
	// Username 是 etcd 访问用户名。
	Username string `json:"account"`
	// Password 是 etcd 访问密码。
	Password string `json:"password"`
	// Endpoint 是 etcd 节点地址列表。
	Endpoint []string `json:"endpoint"`

	// Tls 是可选 TLS 连接配置。
	Tls *tlsx.TLS `json:"tls"`
}
