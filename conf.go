package etcd

type Conf struct {
	Username string   `json:"account"`
	Password string   `json:"password"`
	Endpoint []string `json:"endpoint"`

	Tls *TLS `json:"tls"`
}
