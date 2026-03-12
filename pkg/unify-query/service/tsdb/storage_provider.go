// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// TsDBStorageProvider 定义 TSDB 存储配置提供者接口
// 用于抽象 Consul 和 Redis 的实现，实现多态
type TsDBStorageProvider interface {
	// GetTsDBStorageInfo 获取 TSDB 存储配置信息
	GetTsDBStorageInfo(ctx context.Context) (map[string]any, error)
	// WatchStorageInfo 监听存储配置变更
	WatchStorageInfo(ctx context.Context) (<-chan any, error)
}

// consulStorageProvider Consul 存储提供者实现
type consulStorageProvider struct{}

// GetTsDBStorageInfo 从 Consul 获取 TSDB 存储配置
func (p *consulStorageProvider) GetTsDBStorageInfo(ctx context.Context) (map[string]any, error) {
	consulData, err := consul.GetTsDBStorageInfo()
	if err != nil {
		return nil, err
	}
	// 转换为 map[string]any
	storageData := make(map[string]any, len(consulData))
	for key, value := range consulData {
		storageData[key] = value
	}
	return storageData, nil
}

// WatchStorageInfo 监听 Consul 存储配置变更
func (p *consulStorageProvider) WatchStorageInfo(ctx context.Context) (<-chan any, error) {
	return consul.WatchStorageInfo(ctx)
}

// redisStorageProvider Redis 存储提供者实现
type redisStorageProvider struct{}

// GetTsDBStorageInfo 从 Redis 获取 TSDB 存储配置
func (p *redisStorageProvider) GetTsDBStorageInfo(ctx context.Context) (map[string]any, error) {
	redisData, err := redis.GetTsDBStorageInfo(ctx)
	if err != nil {
		return nil, err
	}
	// 转换为 map[string]any
	storageData := make(map[string]any, len(redisData))
	for key, value := range redisData {
		storageData[key] = value
	}
	return storageData, nil
}

// WatchStorageInfo 监听 Redis 存储配置变更
func (p *redisStorageProvider) WatchStorageInfo(ctx context.Context) (<-chan any, error) {
	return redis.WatchStorageInfo(ctx)
}

// getStorageProvider 根据配置获取存储提供者实例
func getStorageProvider() TsDBStorageProvider {
	if StorageSource == "redis" {
		return &redisStorageProvider{}
	}
	// 默认使用 Consul
	return &consulStorageProvider{}
}

// GetStorageProvider 根据配置获取存储提供者实例
func GetStorageProvider() TsDBStorageProvider {
	return getStorageProvider()
}
