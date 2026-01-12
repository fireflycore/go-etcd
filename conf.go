package etcd

import "github.com/fireflycore/go-utils/tlsx"

type Conf struct {
	Username string   `json:"account"`
	Password string   `json:"password"`
	Endpoint []string `json:"endpoint"`

	Tls *tlsx.TLS `json:"tls"`
}
