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
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	featureFlagPath             = "feature_flag"
	featureFlagChannel          = "feature_flag_channel"
	featureFlagSubscribeTimeout = 5 * time.Second
)

// FeatureFlagClient 处理特性开关相关的 Redis 操作
type FeatureFlagClient struct {
	client   goRedis.UniversalClient
	basePath string
}

// NewFeatureFlagClient 创建特性开关客户端
// client: Redis 客户端实例
// basePath: Redis key 前缀，如 "bkmonitorv3:unify-query"
func NewFeatureFlagClient(client goRedis.UniversalClient, basePath string) *FeatureFlagClient {
	return &FeatureFlagClient{
		client:   client,
		basePath: basePath,
	}
}

// GetFeatureFlagsPath 获取特性开关的 Redis 存储 key
func (f *FeatureFlagClient) GetFeatureFlagsPath() string {
	return fmt.Sprintf("%s:%s:%s", f.basePath, dataPath, featureFlagPath)
}

// GetFeatureFlagsChannel 获取特性开关变更通知的 Redis channel
func (f *FeatureFlagClient) GetFeatureFlagsChannel() string {
	return fmt.Sprintf("%s:%s", f.GetFeatureFlagsPath(), featureFlagChannel)
}

// GetFeatureFlags 从 Redis 获取特性开关配置
func (f *FeatureFlagClient) GetFeatureFlags(ctx context.Context) ([]byte, error) {
	if f.client == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	key := f.GetFeatureFlagsPath()
	data, err := f.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, goRedis.Nil) {
			// Key 不存在时返回 nil，由上层 Feature Flag Provider 回退到 Consul。
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get feature flags from redis: %w", err)
	}

	return []byte(data), nil
}

// WatchFeatureFlags 监听特性开关变更，通过 Redis Pub/Sub 实现
func (f *FeatureFlagClient) WatchFeatureFlags(ctx context.Context) (<-chan any, error) {
	if f.client == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}

	channel := f.GetFeatureFlagsChannel()
	pubSub := f.client.Subscribe(ctx, channel)
	// Receive 不使用客户端 ReadTimeout；握手必须有界，取消时主动关闭连接以中断读取。
	stopClose := context.AfterFunc(ctx, func() { _ = pubSub.Close() })
	defer stopClose()
	if _, err := pubSub.ReceiveTimeout(ctx, featureFlagSubscribeTimeout); err != nil {
		_ = pubSub.Close()
		return nil, fmt.Errorf("failed to subscribe feature flags channel: %w", err)
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
				log.Debugf(ctx, "[redis] watch context cancelled")
				return
			case msg, ok := <-msgChan:
				if !ok {
					log.Debugf(ctx, "[redis] channel closed")
					return
				}
				// 当收到消息时，通知配置变更
				log.Debugf(ctx, "[redis] received change notification: %s", msg.Payload)
				// 非阻塞合并通知；缓冲区已有信号时无需再入队，因为消费者会全量读取。
				select {
				case resultChan <- msg:
				case <-ctx.Done():
					return
				default:
					log.Debugf(ctx, "[redis] feature flag reload notification already pending, coalescing message")
				}
			}
		}
	}()

	return resultChan, nil
}

// InitializeFeatureFlags 仅在 Key 不存在时回填并返回是否创建，避免覆盖并发设置。
func (f *FeatureFlagClient) InitializeFeatureFlags(ctx context.Context, data []byte) (bool, error) {
	if f.client == nil {
		return false, fmt.Errorf("redis client is not initialized")
	}
	created, err := f.client.SetNX(ctx, f.GetFeatureFlagsPath(), data, 0).Result()
	if err != nil {
		return false, fmt.Errorf("failed to initialize feature flags in redis: %w", err)
	}
	if created {
		f.publishFeatureFlags(ctx, data)
	}
	return created, nil
}

// SetFeatureFlags 设置完整特性开关快照到 Redis 并发布变更通知。
func (f *FeatureFlagClient) SetFeatureFlags(ctx context.Context, data []byte) error {
	if f.client == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	key := f.GetFeatureFlagsPath()
	log.Debugf(ctx, "[redis] set feature flags to key: %s", key)

	err := f.client.Set(ctx, key, data, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to set feature flags to redis: %w", err)
	}

	f.publishFeatureFlags(ctx, data)
	return nil
}

// ResetFeatureFlags 删除 Redis 快照并通知在线实例重新读取，由实例完成 Consul 回填。
func (f *FeatureFlagClient) ResetFeatureFlags(ctx context.Context) error {
	if f.client == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	if err := f.client.Del(ctx, f.GetFeatureFlagsPath()).Err(); err != nil {
		return fmt.Errorf("failed to reset feature flags in redis: %w", err)
	}
	f.publishFeatureFlags(ctx, nil)
	return nil
}

func (f *FeatureFlagClient) publishFeatureFlags(ctx context.Context, data []byte) {
	channel := f.GetFeatureFlagsChannel()
	err := f.client.Publish(ctx, channel, string(data)).Err()
	if err != nil {
		log.Errorf(ctx, "[redis] failed to publish feature flags change notification: %s", err)
		// 不返回错误，因为数据变更已经成功
	}
}
