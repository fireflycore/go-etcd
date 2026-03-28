package invocation

import (
	microInvocation "github.com/fireflycore/go-micro/invocation"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// NewConnectionManager 创建基于 etcd Locator 的连接管理器。
//
// 该辅助函数的目标是减少调用方样板代码，让业务侧更容易直接接入：
// - etcd 轻量服务定位
// - go-micro/invocation 的统一连接管理
func NewConnectionManager(client *clientv3.Client, conf *Conf, options microInvocation.ConnectionManagerOptions) (*microInvocation.ConnectionManager, error) {
	locator, err := NewLocator(client, conf)
	if err != nil {
		return nil, err
	}
	options.Locator = locator
	return microInvocation.NewConnectionManager(options)
}
