package etcd

import (
	"errors"
	"time"

	"github.com/fireflycore/go-utils/network"
	"github.com/fireflycore/go-utils/tlsx"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func New(c *Conf) (*clientv3.Client, error) {
	if c == nil {
		return nil, errors.New("etcd: conf is nil")
	}

	config := clientv3.Config{
		DialTimeout: 5 * time.Second,
		Endpoints:   c.Endpoint,
	}

	if c.Username != "" && c.Password != "" {
		config.Username = c.Username
		config.Password = c.Password
	}

	// 从配置生成 TLSConfig；tlsEnabled 表示是否启用 TLS。
	tlsConfig, tlsEnabled, err := tlsx.NewTLSConfig(c.Tls)
	// TLS 配置构造失败时直接返回错误。
	if err != nil {
		return nil, err
	}
	// 启用 TLS 时，将 TLSConfig 写入 clientOptions。
	if tlsEnabled {
		config.TLS = tlsConfig
		// 若未设置 ServerName，则尝试从 Endpoints 中解析第一个主机名作为 ServerName
		if len(c.Endpoint) > 0 {
			host, _, err := network.SplitHostPort(c.Endpoint[0], "2379")
			if err != nil {
				return nil, err
			}
			config.TLS.ServerName = host
		}
	}

	return clientv3.New(config)
}
