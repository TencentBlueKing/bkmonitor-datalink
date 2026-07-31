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
	"sync"

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

type storageKeyScanner interface {
	Scan(ctx context.Context, cursor uint64, match string, count int64) *goRedis.ScanCmd
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
			if errors.Is(err, goRedis.Nil) {
				continue
			}
			return nil, fmt.Errorf("failed to get storage config for key %s: %w", key, err)
		}
		if value == "" {
			return nil, fmt.Errorf("storage config for key %s must not be empty", key)
		}

		var data *Storage
		err = json.Unmarshal([]byte(value), &data)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal storage config for key %s: %w", key, err)
		}
		if data == nil {
			return nil, fmt.Errorf("storage config for key %s must not be null", key)
		}
		result[storageID] = data
	}
	return result, nil
}

func scanStorageKeys(ctx context.Context, client storageKeyScanner, pattern string) ([]string, error) {
	keys := make([]string, 0)
	var cursor uint64
	for {
		batch, nextCursor, err := client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		if nextCursor == 0 {
			return keys, nil
		}
		cursor = nextCursor
	}
}

func (s *StorageClient) scanStorageKeys(ctx context.Context, pattern string) ([]string, error) {
	clusterClient, isCluster := s.client.(*goRedis.ClusterClient)
	if !isCluster {
		return scanStorageKeys(ctx, s.client, pattern)
	}

	keys := make([]string, 0)
	var keysMu sync.Mutex
	err := clusterClient.ForEachMaster(ctx, func(ctx context.Context, master *goRedis.Client) error {
		masterKeys, err := scanStorageKeys(ctx, master, pattern)
		if err != nil {
			return err
		}
		keysMu.Lock()
		keys = append(keys, masterKeys...)
		keysMu.Unlock()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan storage keys from redis cluster: %w", err)
	}
	return keys, nil
}

// GetStorageInfo 从 Redis 获取存储配置信息
// 使用独立的 key 结构，与 Consul 保持一致
func (s *StorageClient) GetStorageInfo(ctx context.Context) (map[string]*Storage, error) {
	if s.client == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	storageKey := s.GetStoragePath()
	pattern := fmt.Sprintf("%s:*", storageKey)

	keys, err := s.scanStorageKeys(ctx, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to scan storage keys from redis: %w", err)
	}

	if len(keys) == 0 {
		return make(map[string]*Storage), nil
	}

	// 使用 FormatStorageInfo 格式化数据
	return s.FormatStorageInfo(keys, func(key string) (string, error) {
		data, err := s.client.Get(ctx, key).Result()
		if err != nil {
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
	pubSub := s.client.Subscribe(ctx, channel)
	if _, err := pubSub.Receive(ctx); err != nil {
		_ = pubSub.Close()
		return nil, fmt.Errorf("failed to subscribe storage channel: %w", err)
	}
	msgChan := pubSub.Channel()

	// 转换为通用的 channel
	// 只保留一个待处理信号：消费者重载期间的连续更新会被合并，但不会全部丢失。
	resultChan := make(chan any, 1)
	go func() {
		defer pubSub.Close()
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
				log.Debugf(ctx, "[redis] received storage change notification")
				// 非阻塞合并通知；缓冲区已有信号时无需再入队，因为消费者会全量读取。
				select {
				case resultChan <- msg:
				case <-ctx.Done():
					return
				default:
					log.Debugf(ctx, "[redis] storage reload notification already pending, coalescing message")
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
var storageClientLock sync.RWMutex

// replaceStorageClient 在 Redis 实例或 Base Path 变化时同步替换缓存客户端。
func replaceStorageClient(client goRedis.UniversalClient, prefix string) {
	storageClientLock.Lock()
	defer storageClientLock.Unlock()
	if client == nil {
		globalStorageClient = nil
		return
	}
	globalStorageClient = NewStorageClient(client, prefix)
}

func getStorageClient() *StorageClient {
	storageClientLock.RLock()
	defer storageClientLock.RUnlock()
	return globalStorageClient
}

// GetStoragePath 获取存储配置的 Redis 存储地址（全局函数）
func GetStoragePath() string {
	storageClient := getStorageClient()
	if storageClient == nil {
		return fmt.Sprintf("%s:%s:%s", basePath, dataPath, storagePath)
	}
	return storageClient.GetStoragePath()
}

// GetStorageInfo 从 Redis 获取存储配置信息（全局函数）
func GetStorageInfo(ctx context.Context) (map[string]*Storage, error) {
	storageClient := getStorageClient()
	if storageClient == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}
	return storageClient.GetStorageInfo(ctx)
}

// WatchStorageInfo 监听 Redis 中的存储配置变更（全局函数）
func WatchStorageInfo(ctx context.Context) (<-chan any, error) {
	storageClient := getStorageClient()
	if storageClient == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}
	return storageClient.WatchStorageInfo(ctx)
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
