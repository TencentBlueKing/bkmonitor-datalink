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
type ProviderManager struct {
	provider SchemaProvider
	logger   Logger
}

// NewProviderManager 根据 providerType 创建并初始化 SchemaProvider。
// providerType: "static" 或 "redis"
// redisClient: redis 模式下必须提供
// staticConfig: static 模式下的硬编码配置
func NewProviderManager(ctx context.Context, logger Logger, providerType string, redisClient redis.UniversalClient, staticConfig StaticProviderConfig) (*ProviderManager, error) {
	if logger == nil {
		logger = &noopLogger{}
	}

	pm := &ProviderManager{logger: logger}

	switch providerType {
	case "static":
		pm.provider = NewStaticSchemaProvider(staticConfig)
		pm.logger.Infof("Using static SchemaProvider")

	case "redis":
		if redisClient == nil {
			return nil, fmt.Errorf("redis client is required for redis provider type")
		}
		p, err := NewRedisProvider(ctx, redisClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create redis SchemaProvider: %w", err)
		}
		pm.provider = p
		pm.logger.Infof("Redis SchemaProvider created successfully")

	default:
		return nil, fmt.Errorf("unknown schema provider type: %s", providerType)
	}

	return pm, nil
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
