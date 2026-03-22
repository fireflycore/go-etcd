package config

import (
	"context"

	microconfig "github.com/fireflycore/go-micro/config"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Watch 监听指定配置键的变更事件。
func (s *StoreInstance) Watch(ctx context.Context, key microconfig.Key) (<-chan microconfig.WatchEvent, error) {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return nil, err
	}

	// 计算 current 路径，并作为 watch 唯一标识。
	currentKey := s.currentKey(key)

	// 为该 watch 创建可取消上下文。
	watchCtx, cancel := context.WithCancel(ctx)

	// 把取消函数记录到 map，供 Unwatch 调用。
	s.watchMu.Lock()
	s.watchCancels[currentKey] = cancel
	s.watchMu.Unlock()

	// 计算输出通道缓冲区大小。
	bufferSize := 8
	if s.options != nil && s.options.WatchBuffer > 0 {
		bufferSize = s.options.WatchBuffer
	}
	out := make(chan microconfig.WatchEvent, bufferSize)

	// 先取一次 current revision，避免漏掉早于 watch 建立前的更新。
	rev := int64(0)
	reqCtx, reqCancel := s.withTimeout(watchCtx)
	res, err := s.client.Get(reqCtx, currentKey)
	reqCancel()
	if err != nil {
		// 建立失败时需要清理取消函数。
		s.watchMu.Lock()
		delete(s.watchCancels, currentKey)
		s.watchMu.Unlock()
		cancel()
		return nil, err
	}
	if res.Header != nil {
		rev = res.Header.Revision + 1
	}

	// 启动异步消费 watch 事件。
	go func() {
		// 关闭前移除取消函数，避免泄漏。
		defer func() {
			s.watchMu.Lock()
			delete(s.watchCancels, currentKey)
			s.watchMu.Unlock()
			close(out)
		}()

		// 构造 watch 参数，删除事件需要拿旧值。
		options := []clientv3.OpOption{clientv3.WithPrevKV()}
		if rev > 0 {
			options = append(options, clientv3.WithRev(rev))
		}

		// 创建 watch 通道并持续消费。
		watchCh := s.client.Watch(watchCtx, currentKey, options...)
		for item := range watchCh {
			// watch 被取消后直接退出。
			if item.Canceled {
				return
			}
			// 逐个处理事件。
			for _, e := range item.Events {
				event, ok := s.toWatchEvent(key, e)
				if !ok {
					continue
				}
				// 发送时支持上下文取消，避免阻塞。
				select {
				case <-watchCtx.Done():
					return
				case out <- event:
				}
			}
		}
	}()

	// 返回监听通道。
	return out, nil
}

// Unwatch 取消指定配置键的监听。
func (s *StoreInstance) Unwatch(key microconfig.Key) {
	// key 不合法时直接忽略，保持幂等。
	if err := validateKey(key); err != nil {
		return
	}

	// 查找对应 cancel 并触发取消。
	currentKey := s.currentKey(key)
	s.watchMu.Lock()
	cancel, ok := s.watchCancels[currentKey]
	s.watchMu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
}

// toWatchEvent 把 etcd 事件转换为统一 watch 事件。
func (s *StoreInstance) toWatchEvent(key microconfig.Key, e *clientv3.Event) (microconfig.WatchEvent, bool) {
	// 初始化默认事件对象。
	raw := microconfig.WatchEvent{
		Key: key,
	}

	// 删除事件优先使用 PrevKv 解析旧值。
	if e.Type == clientv3.EventTypeDelete {
		raw.Type = microconfig.EventDelete
		if e.PrevKv != nil && len(e.PrevKv.Value) > 0 {
			item, err := s.decodeItem(e.PrevKv.Value)
			if err != nil {
				return microconfig.WatchEvent{}, false
			}
			raw.Item = item
		}
		return raw, true
	}

	// Put 事件使用当前 Kv 解析新值。
	raw.Type = microconfig.EventPut
	if e.Kv == nil || len(e.Kv.Value) == 0 {
		return microconfig.WatchEvent{}, false
	}
	item, err := s.decodeItem(e.Kv.Value)
	if err != nil {
		return microconfig.WatchEvent{}, false
	}
	raw.Item = item
	return raw, true
}
