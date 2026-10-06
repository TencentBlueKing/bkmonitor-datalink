// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package featureFlag

import (
	"context"
	"fmt"

	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// FeatureFlagProvider 特性开关提供者接口(consul和redis)
type FeatureFlagProvider interface {
	GetFeatureFlags(ctx context.Context) ([]byte, error)
	WatchFeatureFlags(ctx context.Context) (<-chan any, error)
	GetFeatureFlagsPath() string
}

// fallbackFeatureFlagProvider 优先读取 Redis；Key 缺失时读取并回填 Consul 快照。
// Redis 读取失败直接报错，由 Service 保留最后有效配置。
type fallbackFeatureFlagProvider struct {
	primary    FeatureFlagProvider
	fallback   FeatureFlagProvider
	initialize func(context.Context, []byte) (bool, error)
}

func newFallbackFeatureFlagProvider(primary, fallback FeatureFlagProvider, initialize func(context.Context, []byte) (bool, error)) FeatureFlagProvider {
	return &fallbackFeatureFlagProvider{
		primary:    primary,
		fallback:   fallback,
		initialize: initialize,
	}
}

func (p *fallbackFeatureFlagProvider) GetFeatureFlags(ctx context.Context) ([]byte, error) {
	if p.primary == nil {
		if p.fallback == nil {
			return nil, fmt.Errorf("no feature flag provider is initialized")
		}
		return p.fallback.GetFeatureFlags(ctx)
	}

	data, primaryErr := p.primary.GetFeatureFlags(ctx)
	if primaryErr != nil {
		return nil, primaryErr
	}
	// 仅 nil 表示 Key 不存在。空字符串和无效 JSON 也属于已有配置，应由校验报错。
	if data != nil {
		return data, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if p.fallback == nil {
		return nil, nil
	}

	data, fallbackErr := p.fallback.GetFeatureFlags(ctx)
	if fallbackErr != nil {
		return nil, fallbackErr
	}
	if err := inner.ValidateFeatureFlagSnapshot(data); err != nil {
		return nil, fmt.Errorf("invalid fallback feature flag snapshot: %w", err)
	}
	if p.initialize != nil {
		created, err := p.initialize(ctx, data)
		if err == nil {
			if created {
				return data, nil
			}
			return p.primary.GetFeatureFlags(ctx)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Warnf(ctx, "failed to backfill feature flags to redis, using consul snapshot: %v", err)
	}
	return data, nil
}

func (p *fallbackFeatureFlagProvider) WatchFeatureFlags(ctx context.Context) (<-chan any, error) {
	// 回填后 Redis 为准；Key 缺失或回填失败由 Service 的定时全量调和重试。
	if p.primary != nil {
		return p.primary.WatchFeatureFlags(ctx)
	}
	if p.fallback != nil {
		return p.fallback.WatchFeatureFlags(ctx)
	}
	return nil, fmt.Errorf("no feature flag watcher is initialized")
}

func (p *fallbackFeatureFlagProvider) GetFeatureFlagsPath() string {
	if p.primary != nil {
		return p.primary.GetFeatureFlagsPath()
	}
	if p.fallback != nil {
		return p.fallback.GetFeatureFlagsPath()
	}
	return ""
}
