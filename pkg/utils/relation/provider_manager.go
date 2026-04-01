// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
)

// ProviderManager SchemaProvider 管理器
// 负责创建、管理和关闭 SchemaProvider
type ProviderManager struct {
	provider SchemaProvider
	logger   Logger
}

// NewProviderManager 创建 Provider 管理器
// 如果 logger 为 nil，使用默认的 noop logger
func NewProviderManager(logger Logger) *ProviderManager {
	if logger == nil {
		logger = &noopLogger{}
	}
	return &ProviderManager{
		logger: logger,
	}
}

// InitProvider 根据类型和 Redis 客户端初始化 Provider
// providerType: "static" 或 "redis"
// redisClient: Redis 客户端（当 providerType 为 "redis" 时必须提供）
func (pm *ProviderManager) InitProvider(ctx context.Context, providerType string, redisClient redis.UniversalClient) error {
	// 先关闭旧的 provider
	pm.Close()

	var provider SchemaProvider
	var err error

	switch providerType {
	case "static":
		// static 模式使用空配置的 StaticSchemaProvider 作为兜底
		pm.provider = NewStaticSchemaProvider(StaticProviderConfig{})
		pm.logger.Infof("Using static SchemaProvider (empty config, hardcoded definitions take effect elsewhere)")
		return nil

	case "redis":
		if redisClient == nil {
			return fmt.Errorf("redis client is required for redis provider type")
		}

		// 使用 Redis provider，key 前缀固定为 "bkmonitorv3:entity"
		provider, err = NewRedisProvider(ctx, redisClient)
		if err != nil {
			return fmt.Errorf("failed to create redis SchemaProvider: %w", err)
		}

		pm.logger.Infof("Redis SchemaProvider created successfully")

	default:
		return fmt.Errorf("unknown schema provider type: %s", providerType)
	}

	pm.provider = provider
	return nil
}

// GetProvider 获取当前 Provider
func (pm *ProviderManager) GetProvider() SchemaProvider {
	return pm.provider
}

// Close 关闭 Provider
func (pm *ProviderManager) Close() error {
	if pm.provider != nil {
		if closer, ok := pm.provider.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				pm.logger.Errorf("Failed to close SchemaProvider: %v", err)
				return err
			}
			pm.logger.Infof("SchemaProvider closed successfully")
		}
		pm.provider = nil
	}
	return nil
}
