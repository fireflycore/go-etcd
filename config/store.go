package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	microconfig "github.com/fireflycore/go-micro/config"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	defaultNamespace = "/config-center"
	defaultTenant    = "default"
)

// StoreInstance 是基于 etcd 的统一配置存储实现。
type StoreInstance struct {
	// client 是外部注入的 etcd 客户端。
	client *clientv3.Client
	// options 保存通用配置参数。
	options *microconfig.Options

	// watchMu 用于保护 watchCancels 并发读写。
	watchMu sync.Mutex
	// watchCancels 保存 key 对应的取消函数。
	watchCancels map[string]context.CancelFunc
}

// NewStore 基于 etcd 客户端创建配置存储实例。
func NewStore(client *clientv3.Client, conf *Conf, opts ...microconfig.Option) (*StoreInstance, error) {
	// etcd 客户端为空时直接报错。
	if client == nil {
		return nil, errors.New("etcd config: client is nil")
	}

	// 先构建通用 options，再保存到实例。
	var raw *microconfig.Options
	if conf != nil {
		raw = conf.BuildOptions(opts...)
	} else {
		raw = microconfig.NewOptions(opts...)
	}

	// 返回初始化完成的实例。
	return &StoreInstance{
		client:       client,
		options:      raw,
		watchCancels: make(map[string]context.CancelFunc),
	}, nil
}

// Get 按配置键读取当前生效配置。
func (s *StoreInstance) Get(ctx context.Context, key microconfig.Key) (*microconfig.Item, error) {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return nil, err
	}

	// 基于 options 生成超时上下文，避免慢请求阻塞。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 读取 current 路径。
	res, err := s.client.Get(reqCtx, s.currentKey(key))
	if err != nil {
		return nil, err
	}
	// 未命中时返回统一不存在错误。
	if len(res.Kvs) == 0 {
		return nil, microconfig.ErrResourceNotFound
	}

	// 解析配置内容并返回。
	return s.decodeItem(res.Kvs[0].Value)
}

// GetByQuery 按运行时上下文读取配置。
func (s *StoreInstance) GetByQuery(ctx context.Context, query microconfig.Query) (*microconfig.Item, error) {
	// 复制基础 key，避免修改入参。
	key := query.Key
	// 若 key 未携带租户，则回退到 query.TenantId。
	if key.TenantId == "" {
		key.TenantId = query.TenantId
	}
	// 若 key 未携带 appId，则回退到 query.AppId。
	if key.AppId == "" {
		key.AppId = query.AppId
	}
	// 复用 Get 逻辑，保持行为一致。
	return s.Get(ctx, key)
}

// Put 写入当前生效配置。
func (s *StoreInstance) Put(ctx context.Context, key microconfig.Key, item *microconfig.Item) error {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return err
	}
	// item 为空时直接返回统一错误。
	if item == nil {
		return microconfig.ErrInvalidItem
	}

	// 编码配置内容。
	val, err := s.encodeItem(item)
	if err != nil {
		return err
	}

	// 使用超时上下文执行写入。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 写入 current 路径。
	_, err = s.client.Put(reqCtx, s.currentKey(key), string(val))
	return err
}

// Delete 删除当前配置。
func (s *StoreInstance) Delete(ctx context.Context, key microconfig.Key) error {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return err
	}

	// 使用超时上下文执行删除。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 删除 current 路径。
	_, err := s.client.Delete(reqCtx, s.currentKey(key))
	return err
}

// PutVersion 写入版本快照并返回版本号。
func (s *StoreInstance) PutVersion(ctx context.Context, key microconfig.Key, item *microconfig.Item) (string, error) {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return "", err
	}
	// item 为空时直接返回统一错误。
	if item == nil {
		return "", microconfig.ErrInvalidItem
	}

	// 若调用方未显式提供版本号，则按时间生成版本号。
	version := item.Version
	if version == "" {
		version = time.Now().UTC().Format("20060102150405.000000000")
	}

	// 构造写入版本快照的数据副本。
	versioned := *item
	versioned.Version = version

	// 编码版本内容。
	val, err := s.encodeItem(&versioned)
	if err != nil {
		return "", err
	}

	// 使用超时上下文执行写入。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 写入 versions 路径。
	_, err = s.client.Put(reqCtx, s.versionKey(key, version), string(val))
	if err != nil {
		return "", err
	}

	// 返回最终版本号。
	return version, nil
}

// GetVersion 读取指定版本快照。
func (s *StoreInstance) GetVersion(ctx context.Context, key microconfig.Key, version string) (*microconfig.Item, error) {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return nil, err
	}
	// 版本号为空时返回统一错误。
	if version == "" {
		return nil, microconfig.ErrInvalidItem
	}

	// 使用超时上下文执行读取。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 读取指定版本路径。
	res, err := s.client.Get(reqCtx, s.versionKey(key, version))
	if err != nil {
		return nil, err
	}
	// 未命中时返回统一不存在错误。
	if len(res.Kvs) == 0 {
		return nil, microconfig.ErrResourceNotFound
	}

	// 解析并返回配置内容。
	return s.decodeItem(res.Kvs[0].Value)
}

// ListVersions 列出版本号列表。
func (s *StoreInstance) ListVersions(ctx context.Context, key microconfig.Key, limit int) ([]string, error) {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return nil, err
	}

	// 使用超时上下文执行读取。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 读取版本前缀下的所有条目。
	res, err := s.client.Get(reqCtx, s.versionPrefix(key), clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	// 预分配版本号切片。
	versions := make([]string, 0, len(res.Kvs))
	for _, kv := range res.Kvs {
		// 从 key 尾部提取版本号。
		version := path.Base(string(kv.Key))
		if version == "" {
			continue
		}
		versions = append(versions, version)
	}

	// 按字典序倒排，确保新版本优先。
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))

	// 按 limit 截断结果。
	if limit > 0 && len(versions) > limit {
		versions = versions[:limit]
	}

	// 返回版本号列表。
	return versions, nil
}

// GetMeta 读取配置元信息。
func (s *StoreInstance) GetMeta(ctx context.Context, key microconfig.Key) (*microconfig.Meta, error) {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return nil, err
	}

	// 使用超时上下文执行读取。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 读取 meta 路径。
	res, err := s.client.Get(reqCtx, s.metaKey(key))
	if err != nil {
		return nil, err
	}
	// 未命中时返回统一不存在错误。
	if len(res.Kvs) == 0 {
		return nil, microconfig.ErrResourceNotFound
	}

	// 解析并返回元信息。
	return s.decodeMeta(res.Kvs[0].Value)
}

// PutMeta 写入配置元信息。
func (s *StoreInstance) PutMeta(ctx context.Context, key microconfig.Key, meta *microconfig.Meta) error {
	// key 不合法时直接返回统一错误。
	if err := validateKey(key); err != nil {
		return err
	}
	// meta 为空时返回统一错误。
	if meta == nil {
		return microconfig.ErrInvalidItem
	}

	// 编码元信息。
	val, err := s.encodeMeta(meta)
	if err != nil {
		return err
	}

	// 使用超时上下文执行写入。
	reqCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	// 写入 meta 路径。
	_, err = s.client.Put(reqCtx, s.metaKey(key), string(val))
	return err
}

// withTimeout 基于 options.Timeout 包装上下文。
func (s *StoreInstance) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	// 空上下文回退为 Background。
	if ctx == nil {
		ctx = context.Background()
	}
	// 无超时配置时返回可取消上下文。
	if s.options == nil || s.options.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	// 使用配置的超时时间创建上下文。
	return context.WithTimeout(ctx, s.options.Timeout)
}

// namespace 返回最终使用的命名空间。
func (s *StoreInstance) namespace() string {
	// 默认命名空间用于避免空路径。
	ns := defaultNamespace
	// 允许 options 覆盖默认命名空间。
	if s.options != nil && s.options.Namespace != "" {
		ns = s.options.Namespace
	}
	// 统一前导斜杠风格。
	if !strings.HasPrefix(ns, "/") {
		ns = "/" + ns
	}
	// 去掉尾部斜杠，避免重复分隔符。
	return strings.TrimRight(ns, "/")
}

// normalizeTenant 返回租户路径片段。
func normalizeTenant(tenant string) string {
	// 未设置租户时使用默认租户路径。
	if strings.TrimSpace(tenant) == "" {
		return defaultTenant
	}
	// 去除首尾空格后返回。
	return strings.TrimSpace(tenant)
}

// currentKey 生成 current 配置路径。
func (s *StoreInstance) currentKey(key microconfig.Key) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s/current",
		s.namespace(), normalizeTenant(key.TenantId), key.Env, key.AppId, key.Group, key.Name,
	)
}

// versionPrefix 生成版本路径前缀。
func (s *StoreInstance) versionPrefix(key microconfig.Key) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s/versions",
		s.namespace(), normalizeTenant(key.TenantId), key.Env, key.AppId, key.Group, key.Name,
	)
}

// versionKey 生成指定版本路径。
func (s *StoreInstance) versionKey(key microconfig.Key, version string) string {
	return fmt.Sprintf("%s/%s", s.versionPrefix(key), version)
}

// metaKey 生成元信息路径。
func (s *StoreInstance) metaKey(key microconfig.Key) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s/meta",
		s.namespace(), normalizeTenant(key.TenantId), key.Env, key.AppId, key.Group, key.Name,
	)
}

// encodeItem 对配置内容做编码。
func (s *StoreInstance) encodeItem(item *microconfig.Item) ([]byte, error) {
	// 优先使用调用方注入的编解码器。
	if s.options != nil && s.options.Codec != nil {
		return s.options.Codec.Marshal(item)
	}
	// 默认使用 JSON 编码。
	return json.Marshal(item)
}

// decodeItem 对配置内容做解码。
func (s *StoreInstance) decodeItem(data []byte) (*microconfig.Item, error) {
	// 准备承载结果对象。
	raw := new(microconfig.Item)
	// 优先使用调用方注入的编解码器。
	if s.options != nil && s.options.Codec != nil {
		if err := s.options.Codec.Unmarshal(data, raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	// 默认使用 JSON 解码。
	if err := json.Unmarshal(data, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// encodeMeta 对元信息做编码。
func (s *StoreInstance) encodeMeta(meta *microconfig.Meta) ([]byte, error) {
	// 优先使用调用方注入的编解码器。
	if s.options != nil && s.options.Codec != nil {
		return s.options.Codec.Marshal(meta)
	}
	// 默认使用 JSON 编码。
	return json.Marshal(meta)
}

// decodeMeta 对元信息做解码。
func (s *StoreInstance) decodeMeta(data []byte) (*microconfig.Meta, error) {
	// 准备承载结果对象。
	raw := new(microconfig.Meta)
	// 优先使用调用方注入的编解码器。
	if s.options != nil && s.options.Codec != nil {
		if err := s.options.Codec.Unmarshal(data, raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	// 默认使用 JSON 解码。
	if err := json.Unmarshal(data, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// validateKey 校验配置键合法性。
func validateKey(key microconfig.Key) error {
	// Env 为空时视为无效 key。
	if strings.TrimSpace(key.Env) == "" {
		return microconfig.ErrInvalidKey
	}
	// AppId 为空时视为无效 key。
	if strings.TrimSpace(key.AppId) == "" {
		return microconfig.ErrInvalidKey
	}
	// Group 为空时视为无效 key。
	if strings.TrimSpace(key.Group) == "" {
		return microconfig.ErrInvalidKey
	}
	// Name 为空时视为无效 key。
	if strings.TrimSpace(key.Name) == "" {
		return microconfig.ErrInvalidKey
	}
	// key 校验通过。
	return nil
}
