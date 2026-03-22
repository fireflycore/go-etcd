package config

import (
	"time"

	microconfig "github.com/fireflycore/go-micro/config"
)

// Conf 定义 go-etcd/config 的可选初始化参数。
type Conf struct {
	// Namespace 表示配置存储根路径，如 /config-center。
	Namespace string `json:"namespace"`
	// Timeout 表示单次 etcd 读写超时时间。
	Timeout time.Duration `json:"timeout"`
	// Retry 表示失败重试次数预留字段。
	Retry uint32 `json:"retry"`
	// WatchBuffer 表示 watch 事件缓冲区大小。
	WatchBuffer int `json:"watch_buffer"`
}

// BuildOptions 把 Conf 转换为统一的 micro config options。
func (c *Conf) BuildOptions(opts ...microconfig.Option) *microconfig.Options {
	// 先应用外部传入的 option，保证调用方可覆盖默认值。
	raw := microconfig.NewOptions(opts...)
	// 空配置直接返回外部 option 结果。
	if c == nil {
		return raw
	}
	// 当 Conf 显式配置了命名空间时覆盖。
	if c.Namespace != "" {
		raw.Namespace = c.Namespace
	}
	// 仅当超时时间合法时覆盖默认值。
	if c.Timeout > 0 {
		raw.Timeout = c.Timeout
	}
	// 仅当重试次数合法时覆盖默认值。
	if c.Retry > 0 {
		raw.Retry = c.Retry
	}
	// 仅当缓冲区大小合法时覆盖默认值。
	if c.WatchBuffer > 0 {
		raw.WatchBuffer = c.WatchBuffer
	}
	// 返回最终 options。
	return raw
}
