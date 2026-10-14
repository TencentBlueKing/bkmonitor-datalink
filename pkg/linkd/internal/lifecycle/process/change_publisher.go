// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/alertqueue"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/telemetry"
)

type observedChangePublisher struct {
	queue   *alertqueue.Publisher
	metrics *telemetry.Runtime
}

func (p observedChangePublisher) Publish(ctx context.Context, change domain.AlertChange) error {
	err := p.queue.Publish(ctx, change)
	p.metrics.KACResult(ctx, "enqueue", err, 0, 1)
	return err
}

func changePublisher(cfg config.Config, client redis.UniversalClient, metrics *telemetry.Runtime) lifecycle.AlertChangePublisher {
	if !cfg.Plugins.KACEnabled() {
		return nil
	}
	return observedChangePublisher{queue: &alertqueue.Publisher{Client: client, Stream: alertqueue.Stream(cfg.Dispatch.WithDefaults().Deployment)}, metrics: metrics}
}

// 全局插件开启时，KAC Kafka Hook 已由统一输出链路承接，避免双路处置。
func kacHooks(hooks []config.HookConfig, enabled bool) []config.HookConfig {
	if !enabled {
		return hooks
	}
	out := make([]config.HookConfig, 0, len(hooks))
	for _, h := range hooks {
		if h.Type != config.HookTypeKAC {
			out = append(out, h)
		}
	}
	return out
}
