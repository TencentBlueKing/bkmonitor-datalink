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
	"bytes"
	"context"
	"fmt"
	"sync"
)

// FeatureFlagProvider 特性开关提供者接口(consul和redis)
type FeatureFlagProvider interface {
	GetFeatureFlags(ctx context.Context) ([]byte, error)
	WatchFeatureFlags(ctx context.Context) (<-chan any, error)
	GetFeatureFlagsPath() string
}

// fallbackFeatureFlagProvider 优先从 Redis 读取 Feature Flag，Redis 没有快照
// 或读取失败时再从 Consul 读取。两个 Provider 的 Watch 事件都会触发一次
// 全量调和，以便在 Redis 从无到有时自动切换到 Redis 快照。
type fallbackFeatureFlagProvider struct {
	primary  FeatureFlagProvider
	fallback FeatureFlagProvider
}

func newFallbackFeatureFlagProvider(primary, fallback FeatureFlagProvider) FeatureFlagProvider {
	return &fallbackFeatureFlagProvider{
		primary:  primary,
		fallback: fallback,
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
	// 空字节表示 Redis Key 不存在。{} 是合法的空快照，不能当作 Key 不存在，
	// 否则 metadata 已同步空快照后仍会被旧的 Consul 配置覆盖。
	if primaryErr == nil && len(bytes.TrimSpace(data)) > 0 {
		return data, nil
	}
	if ctx.Err() != nil {
		if primaryErr != nil {
			return nil, primaryErr
		}
		return nil, ctx.Err()
	}

	if p.fallback == nil {
		if primaryErr != nil {
			return nil, fmt.Errorf("primary feature flag provider failed: %w", primaryErr)
		}
		return nil, nil
	}

	data, fallbackErr := p.fallback.GetFeatureFlags(ctx)
	if fallbackErr != nil {
		if primaryErr != nil {
			return nil, fmt.Errorf("primary feature flag provider failed: %v; fallback provider failed: %w", primaryErr, fallbackErr)
		}
		return nil, fallbackErr
	}
	return data, nil
}

func (p *fallbackFeatureFlagProvider) WatchFeatureFlags(ctx context.Context) (<-chan any, error) {
	channels := make([]<-chan any, 0, 2)
	var watchErrors []error

	for _, provider := range []FeatureFlagProvider{p.primary, p.fallback} {
		if provider == nil {
			continue
		}
		channel, err := provider.WatchFeatureFlags(ctx)
		if err != nil {
			watchErrors = append(watchErrors, err)
			continue
		}
		if channel != nil {
			channels = append(channels, channel)
		}
	}

	if len(channels) > 0 {
		return mergeFeatureFlagWatchChannels(ctx, channels...), nil
	}
	if len(watchErrors) == 1 {
		return nil, watchErrors[0]
	}
	if len(watchErrors) == 2 {
		return nil, fmt.Errorf("feature flag watchers unavailable: primary: %v; fallback: %w", watchErrors[0], watchErrors[1])
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

// mergeFeatureFlagWatchChannels 合并 Redis 和 Consul 的通知，并将连续通知合并
// 为一个待处理信号。Service 收到信号后会重新读取 Redis，再按需回退到 Consul。
func mergeFeatureFlagWatchChannels(ctx context.Context, channels ...<-chan any) <-chan any {
	merged := make(chan any, 1)
	var wg sync.WaitGroup
	wg.Add(len(channels))

	for _, channel := range channels {
		go func(channel <-chan any) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-channel:
					if !ok {
						return
					}
					select {
					case merged <- event:
					case <-ctx.Done():
						return
					default:
						// Service 会做全量读取，多个连续通知只需保留一个。
					}
				}
			}
		}(channel)
	}

	go func() {
		wg.Wait()
		close(merged)
	}()

	return merged
}
