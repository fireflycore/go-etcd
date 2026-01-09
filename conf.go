package etcd

import "time"

type Conf struct {
	Username    string        `json:"account"`
	Password    string        `json:"password"`
	Tls         *TLS          `json:"tls"`
	Endpoint    []string      `json:"endpoint"`
	DialTimeout time.Duration `json:"dial_timeout"`
}
