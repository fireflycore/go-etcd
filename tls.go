package etcd

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
)

// TLS 描述数据库连接的 TLS（双向）证书配置。
type TLS struct {
	// CaCert 为 CA 证书文件路径。
	CaCert string `json:"ca_cert"`
	// ClientCert 为客户端证书文件路径。
	ClientCert string `json:"client_cert"`
	// ClientCertKey 为客户端证书私钥文件路径。
	ClientCertKey string `json:"client_cert_key"`
}

// NewTLSConfig 根据 TLS 配置生成 *tls.Config。
func NewTLSConfig(tlsCfg *TLS) (*tls.Config, bool, error) {
	// TLS 配置为空或字段不完整时，认为不启用 TLS。
	if tlsCfg == nil || tlsCfg.CaCert == "" || tlsCfg.ClientCert == "" || tlsCfg.ClientCertKey == "" {
		// 返回 enabled=false，且不视为错误。
		return nil, false, nil
	}

	// certPool 保存信任的 CA 证书。
	certPool := x509.NewCertPool()
	// 读取 CA 证书文件。
	caFile, err := os.ReadFile(tlsCfg.CaCert)
	// 读取失败则返回错误。
	if err != nil {
		return nil, false, err
	}
	// 将 CA 证书追加到 certPool 中。
	if ok := certPool.AppendCertsFromPEM(caFile); !ok {
		return nil, false, errors.New("failed to append ca cert")
	}

	// 加载客户端证书与私钥。
	clientCert, err := tls.LoadX509KeyPair(tlsCfg.ClientCert, tlsCfg.ClientCertKey)
	// 加载失败则返回错误。
	if err != nil {
		return nil, false, err
	}

	// 返回构造完成的 TLS 配置，并标记 enabled=true。
	return &tls.Config{
		// Certificates 为客户端证书链。
		Certificates: []tls.Certificate{clientCert},
		// RootCAs 为服务端证书的信任根。
		RootCAs: certPool,
	}, true, nil
}
