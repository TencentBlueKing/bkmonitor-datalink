// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// StorageInfoProvider 定义存储信息提供者接口
// 用于抽象 Consul 和 Redis 的实现，实现多态
type StorageInfoProvider interface {
	// GetStoragePath 获取存储路径
	GetStoragePath() string
	// GetStorageInfo 获取所有存储配置信息
	GetStorageInfo(ctx context.Context) (map[string]any, error)
	// GetTsDBStorageInfo 获取 TSDB 存储配置信息
	GetTsDBStorageInfo(ctx context.Context) (map[string]any, error)
	// GetStorageName 获取存储提供者名称（用于日志输出）
	GetStorageName() string
}

// consulStorageInfoProvider Consul 存储信息提供者实现
type consulStorageInfoProvider struct{}

// GetStoragePath 获取 Consul 存储路径
func (p *consulStorageInfoProvider) GetStoragePath() string {
	return consul.GetStoragePath()
}

// GetStorageInfo 从 Consul 获取所有存储配置
func (p *consulStorageInfoProvider) GetStorageInfo(ctx context.Context) (map[string]any, error) {
	consulData, err := consul.GetStorageInfo()
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

// GetTsDBStorageInfo 从 Consul 获取 TSDB 存储配置
func (p *consulStorageInfoProvider) GetTsDBStorageInfo(ctx context.Context) (map[string]any, error) {
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

// GetStorageName 获取存储提供者名称
func (p *consulStorageInfoProvider) GetStorageName() string {
	return "consul"
}

// redisStorageInfoProvider Redis 存储信息提供者实现
type redisStorageInfoProvider struct{}

// GetStoragePath 获取 Redis 存储路径
func (p *redisStorageInfoProvider) GetStoragePath() string {
	return redis.GetStoragePath()
}

// GetStorageInfo 从 Redis 获取所有存储配置
func (p *redisStorageInfoProvider) GetStorageInfo(ctx context.Context) (map[string]any, error) {
	redisData, err := redis.GetStorageInfo(ctx)
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

// GetTsDBStorageInfo 从 Redis 获取 TSDB 存储配置
func (p *redisStorageInfoProvider) GetTsDBStorageInfo(ctx context.Context) (map[string]any, error) {
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

// GetStorageName 获取存储提供者名称
func (p *redisStorageInfoProvider) GetStorageName() string {
	return "redis"
}

// getStorageInfoProvider 根据 source 参数获取存储信息提供者实例
func getStorageInfoProvider(source string) StorageInfoProvider {
	if source == "redis" {
		return &redisStorageInfoProvider{}
	}
	// 默认使用 Consul
	return &consulStorageInfoProvider{}
}
