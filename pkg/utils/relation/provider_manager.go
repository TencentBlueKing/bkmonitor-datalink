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

	"github.com/go-redis/redis/v8"
)

// ProviderManager SchemaProvider 管理器
type ProviderManager struct {
	provider SchemaProvider
	logger   Logger
}

// NewProviderManager 创建以内置定义作底层、Redis 作覆盖层的 SchemaProvider，不会失败。
// redisClient 为 nil 时只有内置一层；base 是内置定义，通常为 DefaultStaticProviderConfig()。
func NewProviderManager(ctx context.Context, logger Logger, redisClient redis.UniversalClient, base StaticProviderConfig, opts ...RedisProviderOption) *ProviderManager {
	if logger == nil {
		logger = &noopLogger{}
	}
	options := append([]RedisProviderOption{WithLogger(logger), WithBaseSchema(base)}, opts...)
	pm := &ProviderManager{logger: logger, provider: NewRedisProvider(ctx, redisClient, options...)}
	pm.logger.Infof("layered SchemaProvider created: builtin base with Redis overlay (client configured: %t)", redisClient != nil)
	return pm
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
