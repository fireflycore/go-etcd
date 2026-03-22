package registry

import (
	"github.com/fireflycore/go-micro/constant"
	micro "github.com/fireflycore/go-micro/registry"
)

type ServiceConf struct {
	// 实例Id
	InstanceId string `json:"instance_id"`
	// 命名空间
	Namespace string `json:"namespace"`
	// 网卡
	Network *micro.Network `json:"network"`
	// 内核
	Kernel *micro.Kernel `json:"kernel"`

	// 最大重试次数, 间隔时间是TTL*5
	MaxRetry uint32 `json:"max_retry"`
	// 心跳/租约 TTL（秒）, 最少是10s
	TTL uint32 `json:"ttl"`
	// 权重
	Weight int `json:"weight"`
}

// Bootstrap 补齐 namespace/ttl/maxRetry/network/kernel 等默认值，避免下游逻辑出现零值陷阱
func (sc *ServiceConf) Bootstrap() {
	if sc.Namespace == "" {
		sc.Namespace = constant.DefaultNamespace
	}
	if sc.Weight <= 0 {
		sc.Weight = 100
	}
	if sc.MaxRetry < constant.DefaultMaxRetry {
		sc.MaxRetry = constant.DefaultMaxRetry
	}
	if sc.TTL < constant.DefaultTTL {
		sc.TTL = constant.DefaultTTL
	}

	if sc.Kernel == nil {
		sc.Kernel = &micro.Kernel{}
	}
	sc.Kernel.Bootstrap()

	if sc.Network == nil {
		sc.Network = &micro.Network{}
	}
	sc.Network.Bootstrap()
}

// GatewayConf 定义网关相关配置。
type GatewayConf struct {
	// 网卡
	Network *micro.Network `json:"network"`
}

func (gc *GatewayConf) Bootstrap() {
	if gc.Network == nil {
		gc.Network = &micro.Network{}
	}
	gc.Network.Bootstrap()
}
