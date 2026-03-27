package config

import (
	"context"

	etcd "github.com/fireflycore/go-etcd"
	config "github.com/fireflycore/go-micro/config"
)

// NewStoreFromLoader 基于统一加载参数创建 Etcd 配置存储实例。
// 流程：先按 local / remote 解析出 etcd.Conf，再创建客户端，最后构建 Store。
func NewStoreFromLoader(params config.LoaderParams, localLoad config.LocalLoaderFunc, remoteLoad config.RemoteLoaderFunc, payloadDecode config.PayloadDecodeFunc, conf *Conf, opts ...config.Option) (config.Store, error) {
	backendConf, err := config.LoadConfig[etcd.Conf](params, localLoad, remoteLoad, payloadDecode)
	if err != nil {
		return nil, err
	}

	client, err := etcd.New(&backendConf)
	if err != nil {
		return nil, err
	}

	return NewStore(client, conf, opts...)
}

// LoadConfigFromStore 从 Store 读取当前配置并解码为目标类型 T。
func LoadConfigFromStore[T any](ctx context.Context, store config.Store, params config.StoreParams, payloadDecode config.PayloadDecodeFunc) (T, error) {
	return config.LoadStoreConfig[T](ctx, store, params, payloadDecode)
}
