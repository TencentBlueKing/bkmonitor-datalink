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

// ProviderType 提供器类型
type ProviderType string

const (
	// ProviderTypeStatic 静态提供器
	ProviderTypeStatic ProviderType = "static"
	// ProviderTypeRedis Redis 提供器
	ProviderTypeRedis ProviderType = "redis"
)

// ProviderConfig 提供器配置
type ProviderConfig struct {
	// Type 提供器类型：static 或 redis
	Type ProviderType

	// StaticConfig 静态提供器配置（当 Type = static 时使用）
	StaticConfig *StaticProviderConfig

	// RedisClient Redis 客户端（当 Type = redis 时使用）
	RedisClient redis.UniversalClient

	// RedisOptions Redis 提供器选项（当 Type = redis 时使用）
	RedisOptions []RedisProviderOption
}

// NewSchemaProvider 创建 Schema 提供器（工厂方法）
// 根据配置返回 StaticProvider 或 RedisProvider
func NewSchemaProvider(ctx context.Context, config ProviderConfig) (SchemaProvider, error) {
	switch config.Type {
	case ProviderTypeStatic:
		if config.StaticConfig == nil {
			return nil, fmt.Errorf("static config is required when type is static")
		}
		return NewStaticSchemaProvider(*config.StaticConfig), nil

	case ProviderTypeRedis:
		if config.RedisClient == nil {
			return nil, fmt.Errorf("redis client is required when type is redis")
		}
		return NewRedisProvider(ctx, config.RedisClient, config.RedisOptions...)

	default:
		return nil, fmt.Errorf("unsupported provider type: %s", config.Type)
	}
}
