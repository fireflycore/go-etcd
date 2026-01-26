package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	micro "github.com/fireflycore/go-micro/registry"
	"github.com/fireflycore/go-utils/slicex"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// DiscoverInstance 基于 etcd 的服务发现实例。
type DiscoverInstance struct {
	// mu 保护 method/service 两个内存索引：
	// - GetService 走读锁，允许并发读取
	// - Watcher/bootstrap/adapter 写入时走写锁，保证索引一致
	mu sync.RWMutex

	// ctx/cancel 控制发现实例的生命周期：
	// - Watcher 会阻塞运行，收到 ctx.Done() 后退出
	// - Unwatch() 调用 cancel() 触发退出
	ctx    context.Context
	cancel context.CancelFunc

	// client 为外部注入的 etcd v3 客户端
	client *clientv3.Client

	meta *micro.Meta
	conf *micro.ServiceConf

	// service 是发现的“主表”：appId -> 节点列表
	// method 是 service 的“派生索引”：method -> appId，用于 GetService 快速定位
	// 约束：method 必须始终能由 service 推导得到（通过 refreshMethodsLocked 保持一致）
	method  micro.ServiceMethod
	service micro.ServiceDiscover

	log *zap.Logger

	// watchRev 用于衔接 bootstrap() 与 Watcher()：
	// - bootstrap() 通过一次 Get 拉取快照，同时拿到该次 Get 的 revision
	// - Watcher 从 revision+1 开始 watch，尽量避免“Get 完成到 Watch 建立”之间的事件丢失
	watchRev int64
}

// NewDiscover 创建基于 etcd 的服务发现实例。
// 参数:
//   - client: etcd客户端实例
//   - meta: 服务元数据信息
//   - config: 服务配置信息
//
// 返回:
//   - micro.Discovery: 服务发现接口实现
//   - error: 错误信息
func NewDiscover(client *clientv3.Client, meta *micro.Meta, conf *micro.ServiceConf) (micro.Discovery, error) {
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

	// 创建可取消的上下文，用于优雅关; cancel 会被 Unwatch 调用
	ctx, cancel := context.WithCancel(context.Background())

	// 初始化服务发现实例。
	instance := &DiscoverInstance{
		ctx:    ctx,
		cancel: cancel,
		client: client,

		meta: meta,
		conf: conf,

		method:  make(micro.ServiceMethod),
		service: make(micro.ServiceDiscover),
	}

	err := instance.bootstrap()
	if err != nil {
		return nil, err
	}

	return instance, nil
}

// GetService 根据 gRPC 方法名获取对应的服务节点列表。
// 参数:
//   - sm: 服务方法名
//
// 返回:
//   - []*micro.ServiceNode: 服务节点列表
//   - error: 错误信息，当服务方法不存在时返回错误
func (s *DiscoverInstance) GetService(sm string) ([]*micro.ServiceNode, error) {
	// 读锁：允许并发读取，但禁止与写入并发
	s.mu.RLock()

	// 通过方法名定位到所属 appId
	appId, ok := s.method[sm]
	if !ok {
		s.mu.RUnlock()
		return nil, micro.ErrServiceMethodNotExists
	}

	// 根据 appId 取出节点列表
	nodes, ok := s.service[appId]
	if !ok {
		s.mu.RUnlock()
		return nil, micro.ErrServiceNodeNotExists
	}
	// 返回 slice 的副本，避免调用方持有内部切片导致并发读写风险；节点指针本身仍共享。
	out := append([]*micro.ServiceNode(nil), nodes...)

	s.mu.RUnlock()

	return out, nil
}

// Watcher 启动服务发现监控。
// 该方法会阻塞执行，持续监控 etcd 中的服务变化，通常在单独的 goroutine 中调用。
func (s *DiscoverInstance) Watcher() {
	// watchKey 只监听当前命名空间 + 环境：
	// key 结构与注册侧保持一致：/{namespace}/{env}/{appId}/{leaseId}
	watchKey := fmt.Sprintf("%s/%s", s.conf.Namespace, s.meta.Env)

	// startRev 是本次 Watch 的起始 revision：
	// - 第一次 Watch：来自 bootstrap() 填充（Get 的 revision+1）
	// - Watch 运行中：随事件推进不断前移（header.revision+1）
	// - 发生 compact：从 CompactRevision+1 重新 watch
	startRev := s.watchRev
	for {
		select {
		case <-s.ctx.Done():
			// 外部 Unwatch() 调用 cancel 后，Watcher 退出
			return
		default:
			// 不阻塞：继续往下创建/消费 watch 流
		}

		// WithPrevKV 让 delete 事件携带旧值（PrevKv），便于我们从 value 里解析出 appId/leaseId/methods。
		opts := []clientv3.OpOption{clientv3.WithPrefix(), clientv3.WithPrevKV()}
		if startRev > 0 {
			// 从指定 revision 开始，避免漏事件/重复消费
			opts = append(opts, clientv3.WithRev(startRev))
		}

		// 创建 watch 流（返回一个 channel）
		wc := s.client.Watch(s.ctx, watchKey, opts...)
		// 遍历 Watch 响应流
		for v := range wc {
			select {
			case <-s.ctx.Done():
				// 外部调用 Unwatch() 或 ctx 被取消时，退出整个监控循环
				return
			default:
				// 不阻塞：继续处理该条 watch 响应
			}

			if v.Canceled {
				// Watch 被取消常见原因：
				// - 网络/鉴权/集群不可用：v.Err() 非空
				// - compact：CompactRevision > 0，需要从更高 revision 重新 watch
				if s.log != nil && v.Err() != nil {
					s.log.Error("etcd watch canceled", zap.Error(v.Err()))
				}
				// 发生 compact：从压缩点之后开始重放
				if v.CompactRevision > 0 {
					// 等同于“跳过已被压缩的历史”
					startRev = v.CompactRevision + 1
					goto restart
				}
				if v.Header.Revision > 0 {
					// 尝试基于最新 revision 推进起点
					nextRev := v.Header.Revision + 1
					if nextRev > startRev {
						// 只前进不回退
						startRev = nextRev
					}
				}
				goto restart
			}

			// 推进 startRev，避免 watch 重建后重复消费已经处理过的 revision。
			if v.Header.Revision > 0 {
				// 下一次从该 revision+1 开始
				nextRev := v.Header.Revision + 1
				// 只前进不回退
				if nextRev > startRev {
					startRev = nextRev
				}
			}

			// 遍历当前响应中的所有事件
			for _, e := range v.Events {
				// adapter 内部会做反序列化，并按 Put/Delete 更新 service/method 两张表，单事件处理（内部会写锁保护）
				s.adapter(e)
			}
		}

		// restart 统一用于“需要重建 watch 流”的场景（包括 compact 和一般 cancel）。
	restart:
		// 极小退避，避免异常时空转
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-s.ctx.Done():
			// 提前停止 timer，避免泄漏
			timer.Stop()
			return
		case <-timer.C:
			// 退避结束，进入下一轮重建 watch
		}
	}
}

// Unwatch 停止服务发现监控并释放资源
// 调用此方法会取消上下文，停止所有的监控goroutine
func (s *DiscoverInstance) Unwatch() {
	// 调用 cancel 触发 ctx.Done()，所有使用该 ctx 的协程都会退出
	s.cancel()
}

// WithLog 设置日志记录函数
// 参数:
//   - handle: 日志处理函数，接收日志级别和消息内容
func (s *DiscoverInstance) WithLog(log *zap.Logger) {
	s.log = log
}

// bootstrap 初始化引导
// 从etcd中加载现有的服务注册信息，构建初始的服务发现数据
// 返回:
//   - error: 初始化过程中发生的错误
func (s *DiscoverInstance) bootstrap() error {
	// 从etcd获取指定命名空间、环境下的所有键值对， // 与注册侧 key 结构保持一致
	prefix := fmt.Sprintf("%s/%s", s.conf.Namespace, s.meta.Env)
	// 拉取快照（一次 Get）
	res, err := s.client.Get(s.ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return err
	}

	// 记录当前的 revision，Watch 时从 revision+1 开始监听，
	// 以此尽量保证“本次 Get 到的数据”与“后续 Watch 事件”在时间线上连贯。
	if res.Header != nil {
		s.watchRev = res.Header.Revision + 1
	}

	// 遍历所有获取到的键值对
	for _, item := range res.Kvs {
		// 注意：这里用值类型接收反序列化结果
		var val micro.ServiceNode
		if err = json.Unmarshal(item.Value, &val); err == nil {
			if val.Meta == nil || val.Meta.AppId == "" || val.Meta.Env == "" {
				// 过滤不完整数据
				continue
			}

			s.mu.Lock()
			// upsertNodeLocked 会同步刷新 method 索引，保证 method 与 service 一致
			s.upsertNodeLocked(val.Meta.AppId, &val)
			s.mu.Unlock()
		}
	}

	// 记录初始化完成日志
	if s.log != nil {
		s.log.Info("bootstrap completed", zap.Int("services", len(s.service)))
	}

	return nil
}

// adapter 服务发现适配器
// 将etcd的原始事件转换为服务发现内部事件
// 参数:
//   - e: etcd事件，包含事件类型和键值信息
func (s *DiscoverInstance) adapter(e *clientv3.Event) {
	// 对于 Put/Delete 事件，etcd 返回的 value 来源不同：
	// - Put：从 e.Kv.Value 读取最新值
	// - Delete：从 e.PrevKv.Value 读取被删除前的旧值（需要 Watch 时启用 WithPrevKV）
	var tv []byte
	switch e.Type {
	case clientv3.EventTypeDelete:
		if e.PrevKv == nil {
			// 没有旧值无法解析
			return
		}
		// Delete 用旧值
		tv = e.PrevKv.Value
	default:
		if e.Kv == nil {
			// 没有新值无法解析
			return
		}
		// Put 用新值
		tv = e.Kv.Value
	}

	var val micro.ServiceNode
	if err := json.Unmarshal(tv, &val); err != nil {
		if s.log != nil {
			s.log.Error("failed to unmarshal service node", zap.Error(err))
		}
		return
	}
	if val.Meta == nil || val.Meta.AppId == "" || val.Meta.Env == "" {
		// 防御脏数据
		return
	}

	// 注意：这里不直接把 val.Methods 写入 method 映射表，而是把变更落到 service 后，
	// 通过 upsert/delete 内部的 refreshMethodsLocked(appId) 统一重建 method 索引，
	// 避免出现“节点 methods 发生缩减但 method 表仍然残留旧方法”的陈旧状态。
	s.mu.Lock()
	switch e.Type {
	case clientv3.EventTypePut: // 新增或更新服务节点
		s.upsertNodeLocked(val.Meta.AppId, &val) // 合并（按 leaseId 去重）
	case clientv3.EventTypeDelete: // 删除服务节点
		s.deleteNodeLocked(val.Meta.AppId, &val) // 删除（按 leaseId 匹配）
	}
	s.mu.Unlock()
}

func (s *DiscoverInstance) upsertNodeLocked(appId string, newNode *micro.ServiceNode) {
	nodes := s.service[appId]
	// 过滤同 leaseId 的旧节点
	nodes = slicex.FilterSlice(nodes, func(_ int, item *micro.ServiceNode) bool {
		return item.LeaseId != newNode.LeaseId
	})
	// 新节点放在前面，优先返回
	s.service[appId] = append([]*micro.ServiceNode{newNode}, nodes...)
	// 统一重建 method 派生索引
	s.refreshMethodsLocked(appId)

	if s.log != nil {
		s.log.Info("service updated", zap.String("appId", appId), zap.Int("leaseId", newNode.LeaseId), zap.Int("nodesCount", len(s.service[appId])))
	}
}

func (s *DiscoverInstance) deleteNodeLocked(appId string, removedNode *micro.ServiceNode) {
	// 删除前数量
	originalCount := len(s.service[appId])
	// 过滤目标 leaseId
	s.service[appId] = slicex.FilterSlice(s.service[appId], func(_ int, item *micro.ServiceNode) bool {
		return item.LeaseId != removedNode.LeaseId
	})

	if s.log != nil {
		// 删除后的节点数
		remainingCount := len(s.service[appId])
		if originalCount != remainingCount {
			s.log.Info("service removed", zap.String("appId", appId), zap.Int("leaseId", removedNode.LeaseId), zap.Int("beforeCount", originalCount), zap.Int("afterCount", remainingCount))
		}
	}

	if len(s.service[appId]) == 0 {
		// 节点清空则移除该 appId 主表项
		delete(s.service, appId)
	}
	// 统一重建 method 派生索引（会清掉该 appId 的旧映射）
	s.refreshMethodsLocked(appId)

	if s.log != nil && len(s.service[appId]) == 0 {
		s.log.Info("service has no nodes, removed from discovery", zap.String("appId", appId))
	}
}

func (s *DiscoverInstance) refreshMethodsLocked(appId string) {
	// method 是 service 的派生索引，这里用“先清空该 appId 的映射，再按现存节点重建”的方式
	// 保证 method 不会遗留过期方法（比如某节点更新后 methods 变少的场景）。
	for sm, owner := range s.method {
		if owner == appId {
			// 先清理该 appId 的所有旧方法映射
			delete(s.method, sm)
		}
	}

	for _, node := range s.service[appId] {
		if node == nil || node.Meta == nil || node.Meta.AppId == "" {
			continue
		}
		for sm := range node.Methods {
			// 把当前节点提供的方法重新挂回 appId
			s.method[sm] = appId
		}
	}
}
