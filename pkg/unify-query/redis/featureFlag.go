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
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	featureFlagPath    = "feature_flag"
	featureFlagChannel = "feature_flag_channel"
)

// GetFeatureFlagsPath 获取特性开关的 Redis 存储 key
func GetFeatureFlagsPath() string {
	return fmt.Sprintf("%s:%s:%s", basePath, dataPath, featureFlagPath)
}

// GetFeatureFlagsChannel 获取特性开关变更通知的 Redis channel
func GetFeatureFlagsChannel() string {
	return fmt.Sprintf("%s:%s", GetFeatureFlagsPath(), featureFlagChannel)
}

// GetFeatureFlags 从 Redis 获取特性开关配置
func GetFeatureFlags(ctx context.Context) ([]byte, error) {
	return GetKVData(ctx, GetFeatureFlagsPath())
}

// WatchFeatureFlags 监听特性开关变更，通过 Redis Pub/Sub 实现
func WatchFeatureFlags(ctx context.Context) (<-chan any, error) {
	return WatchChange(ctx, GetFeatureFlagsChannel())
}

// SetFeatureFlags 设置特性开关配置到 Redis 并发布变更通知 (mock测试)
func SetFeatureFlags(ctx context.Context, data []byte) error {
	client := Client()
	if client == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	key := GetFeatureFlagsPath()
	log.Debugf(ctx, "[redis] set feature flags to key: %s", key)

	err := client.Set(ctx, key, data, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to set feature flags to redis: %w", err)
	}

	// 发布变更通知，将配置内容作为消息
	channel := GetFeatureFlagsChannel()
	err = client.Publish(ctx, channel, string(data)).Err()
	if err != nil {
		log.Errorf(ctx, "[redis] failed to publish feature flags change notification: %s", err)
		// 不返回错误，因为数据已经设置成功
	}

	return nil
}
