// Package etcd 提供基于 etcd 的服务注册与服务发现实现。
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	micro "github.com/fireflycore/go-micro/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// RegisterInstance 基于 etcd 的服务注册实例
type RegisterInstance struct {
	// ctx/cancel 控制注册实例生命周期：
	// - SustainLease 会阻塞运行，收到 ctx.Done() 后退出
	// - Uninstall() 调用 cancel() 并撤销 lease
	ctx    context.Context
	cancel context.CancelFunc

	// client 为外部注入的 etcd v3 客户端
	client *clientv3.Client
	// lease 为当前服务节点绑定的租约 ID；租约存在时 key 才会保活
	lease clientv3.LeaseID

	meta *micro.Meta
	conf *micro.ServiceConf

	// 当前已重试次数
	retryCount uint32
	// 重试前的回调
	retryBefore func()
	// 重试成功后的回调
	retryAfter func()

	log *zap.Logger

	// lastNode 缓存最后一次注册的服务节点信息：
	// - KeepAlive 断线/租约失效后，会重新申请 lease
	// - 新 lease 下必须重新 Put 服务节点数据（旧 lease 到期后 key 会被 etcd 自动删除）
	lastNode *micro.ServiceNode
}

// NewRegister 创建基于 etcd 的服务注册实例。
func NewRegister(client *clientv3.Client, meta *micro.Meta, conf *micro.ServiceConf) (micro.Register, error) {
	if client == nil {
		return nil, fmt.Errorf(micro.ErrClientIsNil, "etcd")
	}
	if meta == nil {
		return nil, micro.ErrServiceMetaIsNil
	}
	if conf == nil {
		return nil, micro.ErrServiceConfIsNil
	}
	conf.Bootstrap()

	// 创建可取消的上下文，用于优雅关; cancel 会被 Uninstall 调用
	ctx, cancel := context.WithCancel(context.Background())

	instance := &RegisterInstance{
		ctx:    ctx,
		cancel: cancel,

		client: client,

		meta: meta,
		conf: conf,
	}

	// 预先创建 lease，Install 时即可绑定
	err := instance.initLease()
	if err != nil {
		return nil, err
	}

	return instance, nil
}

// Install 将服务节点写入注册中心，并绑定到当前 lease。
func (s *RegisterInstance) Install(service *micro.ServiceNode) error {
	if service == nil {
		return micro.ErrServiceNodeIsNil
	}

	// Install 负责补齐节点的运行时信息；Methods 通常由上层从 gRPC 描述解析得到
	service.Meta = s.meta
	service.Kernel = s.conf.Kernel
	service.Network = s.conf.Network
	service.LeaseId = int(s.lease)
	service.Weight = s.conf.Weight
	service.RunDate = time.Now().Format(time.DateTime)

	// 缓存节点信息，用于后续重试/重连时重新注册，续租失败后，使用它重新 Put
	s.lastNode = service

	return s.register()
}

// register 执行实际的 etcd put 操作
func (s *RegisterInstance) register() error {
	if s.lastNode == nil {
		// 没有缓存的节点信息时不做任何写入（理论上 Install 会先设置 lastNode）
		return nil
	}

	// value 存 JSON 序列化后的 ServiceNode，发现端会反序列化并更新本地缓存
	val, err := json.Marshal(s.lastNode)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()

	// key 结构：/{namespace}/{env}/{appId}/{leaseId}
	// - 只要 lease 失效，etcd 会自动删除该 key，发现端会收到 delete 事件
	key := fmt.Sprintf("%s/%s/%s/%d", s.conf.Namespace, s.meta.Env, s.meta.AppId, s.lease)
	_, err = s.client.Put(ctx, key, string(val), clientv3.WithLease(s.lease))

	return err
}

// Uninstall 撤销 lease 并停止续约。
func (s *RegisterInstance) Uninstall() {
	// 先让 SustainLease 退出
	s.cancel()

	// 撤销 lease 限时
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// best-effort：撤销失败也不影响进程退出
	_, _ = s.client.Revoke(ctx, s.lease)
}

// WithLog 设置内部日志输出回调。
func (s *RegisterInstance) WithLog(log *zap.Logger) {
	s.log = log
}

// WithRetryBefore 设置重试前回调。
func (s *RegisterInstance) WithRetryBefore(handle func()) {
	s.retryBefore = handle
}

// WithRetryAfter 设置重试成功后回调。
func (s *RegisterInstance) WithRetryAfter(handle func()) {
	s.retryAfter = handle
}

// initLease 初始化租约，一个带 TTL 的 lease；后续 Put 时把 key 绑定到该 lease 上
func (s *RegisterInstance) initLease() error {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	grant, err := s.client.Grant(ctx, int64(s.conf.TTL))
	if err != nil {
		return err
	}
	s.lease = grant.ID

	return nil
}

// SustainLease 持续续约，直到上下文被取消。
func (s *RegisterInstance) SustainLease() {
	// KeepAlive 会返回一个 channel，etcd client 会持续往里写心跳响应。
	// 该 channel 被关闭通常意味着：
	// - 网络断开 / server 不可用
	// - lease 已不存在（过期/被撤销）
	// 此时进入 retryLease 逻辑：重新获取 lease 并重新注册节点数据。
	for {
		select {
		case <-s.ctx.Done():
			// Uninstall() 调用 cancel 后，优雅退出
			return
		default:
			// 不阻塞：尝试建立/消费 keepalive channel
		}

		// 建立 keepalive 流
		leaseCh, err := s.client.KeepAlive(s.ctx, s.lease)
		if err != nil || leaseCh == nil {
			// KeepAlive 创建失败：进入重试流程（重建 lease + 重新注册）
			if !s.retryLease() {
				return
			}
			continue
		}

		for {
			select {
			case <-s.ctx.Done():
				return
			case _, ok := <-leaseCh: // 阻塞等待下一次续租响应
				if !ok {
					// leaseCh 关闭：认为 keepalive 已不可用，进入重试流程
					if !s.retryLease() {
						return
					}
					goto next
				}
				// 收到任意一次续约响应，就认为当前链路恢复正常，把重试计数清零
				if s.retryCount != 0 {
					s.retryCount = 0
				}
			}
		}
	next:
	}
}

func (s *RegisterInstance) retryLease() bool {
	// 重试策略：
	// - 每次间隔 TTL*5 秒（避免频繁打满 etcd / 网络）
	// - 重新 Grant lease
	// - 若有 lastNode，则用新 leaseId 重新注册节点 key/value
	// - 达到 MaxRetry 次仍失败则放弃（SustainLease 退出）
	for s.retryCount < s.conf.MaxRetry {
		if s.retryBefore != nil {
			s.retryBefore()
		}

		// TTL最小为10s, 退避时间TTL*5
		timer := time.NewTimer(time.Duration(s.conf.TTL) * 5 * time.Second)
		select {
		case <-s.ctx.Done():
			// 提前停止 timer，避免泄漏
			timer.Stop()
			return false
		case <-timer.C:
			// 退避结束，开始本轮重试
		}

		// 计数：本轮重试开始
		s.retryCount++

		if s.log != nil {
			s.log.Info("etcd retry lease", zap.Uint32("retryCount", s.retryCount), zap.Uint32("maxRetry", s.conf.MaxRetry))
		}

		// 重新获取 lease，Grant 失败：继续下一轮退避重试
		if err := s.initLease(); err != nil {
			continue
		}

		// 获取新 lease 成功后，必须重新注册服务数据
		// 因为旧 lease 过期后，数据会被 etcd 删除
		if s.lastNode != nil {
			// 更新 leaseId
			s.lastNode.LeaseId = int(s.lease)

			// Put 失败：继续下一轮重试
			if err := s.register(); err != nil {
				if s.log != nil {
					s.log.Error("etcd re-register failed", zap.Error(err))
				}
				continue
			}
		}

		if s.retryAfter != nil {
			s.retryAfter()
		}

		// 本次重试成功，清零计数
		s.retryCount = 0
		return true
	}

	return false
}
