// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// NewRedisInstance : https://redis.uptrace.dev/guide/universal.html
// If the MasterName option is specified, a sentinel-backed FailoverClient is returned.
// if the number of Addrs is two or more, a ClusterClient is returned.
// Otherwise, a single-node Client is returned.

const (
	storagePath    = "storage"
	storageChannel = "storage_channel"
)

// Storage 存储配置结构体
type Storage struct {
	Address  string `json:"address"`
	Username string `json:"username"`
	Password string `json:"password"`
	Type     string `json:"type"`
}

// StorageClient 处理存储配置相关的 Redis 操作
type StorageClient struct {
	client goRedis.UniversalClient
	prefix string
}

// NewStorageClient 创建存储配置客户端
// client: Redis 客户端实例
// basePath: Redis key 前缀，如 "bkmonitorv3:unify-query"
func NewStorageClient(client goRedis.UniversalClient, basePath string) *StorageClient {
	return &StorageClient{
		client: client,
		prefix: basePath,
	}
}

// GetStoragePath 获取存储配置的 Redis 存储 key 前缀
func (s *StorageClient) GetStoragePath() string {
	return fmt.Sprintf("%s:%s:%s", s.prefix, dataPath, storagePath)
}

// GetStorageChannel 获取存储配置变更通知的 Redis channel
func (s *StorageClient) GetStorageChannel() string {
	return fmt.Sprintf("%s:%s", s.GetStoragePath(), storageChannel)
}

// FormatStorageInfo 格式化存储配置信息
// 从 Redis 独立 key 的数据中解析出 Storage 结构（类似 Consul 的 FormatStorageInfo）
func (s *StorageClient) FormatStorageInfo(keys []string, getValue func(string) (string, error)) (map[string]*Storage, error) {
	result := make(map[string]*Storage)
	storageKey := s.GetStoragePath()
	prefix := storageKey + ":"

	for _, key := range keys {
		// 提取 storageID
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		storageID := strings.TrimPrefix(key, prefix)
		if storageID == "" || storageID == key {
			continue
		}

		value, err := getValue(key)
		if err != nil {
			continue
		}
		if value == "" {
			continue
		}

		var data *Storage
		err = json.Unmarshal([]byte(value), &data)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal storage config for key %s: %w", key, err)
		}
		result[storageID] = data
	}
	return result, nil
}

// GetStorageInfo 从 Redis 获取存储配置信息
// 使用独立的 key 结构，与 Consul 保持一致
func (s *StorageClient) GetStorageInfo(ctx context.Context) (map[string]*Storage, error) {
	if s.client == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	storageKey := s.GetStoragePath()
	pattern := fmt.Sprintf("%s:*", storageKey)

	keys, err := s.client.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get storage keys from redis: %w", err)
	}

	if len(keys) == 0 {
		return make(map[string]*Storage), nil
	}

	// 使用 FormatStorageInfo 格式化数据
	return s.FormatStorageInfo(keys, func(key string) (string, error) {
		data, err := s.client.Get(ctx, key).Result()
		if err != nil {
			if errors.Is(err, goRedis.Nil) {
				return "", nil
			}
			return "", fmt.Errorf("failed to get storage value for key %s: %w", key, err)
		}
		return data, nil
	})
}

// WatchStorageInfo 监听 Redis 中的存储配置变更
// 使用 Redis Pub/Sub 机制监听配置变更
func (s *StorageClient) WatchStorageInfo(ctx context.Context) (<-chan any, error) {
	if s.client == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	channel := s.GetStorageChannel()
	msgChan := s.client.Subscribe(ctx, channel).Channel()

	// 转换为通用的 channel
	resultChan := make(chan any)
	go func() {
		defer close(resultChan)
		for {
			select {
			case <-ctx.Done():
				log.Debugf(ctx, "[redis] watch storage context cancelled")
				return
			case msg, ok := <-msgChan:
				if !ok {
					log.Debugf(ctx, "[redis] storage channel closed")
					return
				}
				// 当收到消息时，通知配置变更
				log.Debugf(ctx, "[redis] received storage change notification: %s", msg.Payload)
				// 使用非阻塞发送，如果接收者已停止，直接丢弃消息
				select {
				case resultChan <- msg:
				case <-ctx.Done():
					return
				default:
					// 如果 resultChan 已满或接收者已停止，记录日志但不阻塞
					log.Debugf(ctx, "[redis] result channel is full or receiver stopped, dropping message")
				}
			}
		}
	}()

	return resultChan, nil
}

// SetStorage 设置存储配置到 Redis 并发布变更通知（主要用于测试）
func (s *StorageClient) SetStorage(ctx context.Context, storageID string, storage *Storage) error {
	if s.client == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	key := fmt.Sprintf("%s:%s", s.GetStoragePath(), storageID)
	data, err := json.Marshal(storage)
	if err != nil {
		return fmt.Errorf("failed to marshal storage config: %w", err)
	}

	log.Debugf(ctx, "[redis] set storage to key: %s", key)

	err = s.client.Set(ctx, key, data, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to set storage to redis: %w", err)
	}

	// 发布变更通知
	channel := s.GetStorageChannel()
	err = s.client.Publish(ctx, channel, string(data)).Err()
	if err != nil {
		log.Errorf(ctx, "[redis] failed to publish storage change notification: %s", err)
		// 不返回错误，因为数据已经设置成功
	}

	return nil
}

// 全局函数包装，使用全局 Redis 实例
var globalStorageClient *StorageClient

// initStorageClient 初始化全局存储客户端
func initStorageClient() {
	if globalInstance != nil && globalInstance.client != nil {
		globalStorageClient = NewStorageClient(globalInstance.client, basePath)
	}
}

// GetStoragePath 获取存储配置的 Redis 存储地址（全局函数）
func GetStoragePath() string {
	if globalStorageClient == nil {
		initStorageClient()
	}
	if globalStorageClient == nil {
		return fmt.Sprintf("%s:%s:%s", basePath, dataPath, storagePath)
	}
	return globalStorageClient.GetStoragePath()
}

// GetStorageInfo 从 Redis 获取存储配置信息（全局函数）
func GetStorageInfo(ctx context.Context) (map[string]*Storage, error) {
	if globalStorageClient == nil {
		initStorageClient()
	}
	if globalStorageClient == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}
	return globalStorageClient.GetStorageInfo(ctx)
}

// WatchStorageInfo 监听 Redis 中的存储配置变更（全局函数）
func WatchStorageInfo(ctx context.Context) (<-chan any, error) {
	if globalStorageClient == nil {
		initStorageClient()
	}
	if globalStorageClient == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}
	return globalStorageClient.WatchStorageInfo(ctx)
}

// GetInfluxdbStorageInfo 获取 influxdb 存储实例
func GetInfluxdbStorageInfo(ctx context.Context) (map[string]*Storage, error) {
	infos, err := GetStorageInfo(ctx)
	if err != nil {
		return nil, err
	}
	influxdbInfos := make(map[string]*Storage)
	for key, info := range infos {
		if info.Type != metadata.InfluxDBStorageType {
			continue
		}
		influxdbInfos[key] = info
	}
	return influxdbInfos, nil
}

// GetESStorageInfo 获取 elasticsearch 存储实例
func GetESStorageInfo(ctx context.Context) (map[string]*Storage, error) {
	infos, err := GetStorageInfo(ctx)
	if err != nil {
		return nil, err
	}
	esInfos := make(map[string]*Storage)
	for key, info := range infos {
		if info.Type != metadata.ElasticsearchStorageType {
			continue
		}
		esInfos[key] = info
	}
	return esInfos, nil
}
