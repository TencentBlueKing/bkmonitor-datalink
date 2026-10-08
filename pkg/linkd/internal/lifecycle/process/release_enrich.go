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
	"errors"
	"fmt"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
	"linkd/internal/lifecycle"
)

// releaseEnricher 按 Event 固定的来源发布版本执行。当前任务版本复用连接，其他版本用有界临时运行时。
// 不把旧 Event 默默交给新配置；历史运行时只在本次调用内有效，关闭后释放全部连接。
type releaseEnricher struct {
	current config.EventSource
	engine  lifecycle.EventEnricher
	read    func(context.Context, string, int64) (config.EventSource, error)
	open    func(context.Context, config.EventSource) (lifecycle.EventEnricher, func() error, error)
	slots   chan struct{}
}

func (r *releaseEnricher) Enrich(ctx context.Context, input enrich.Input) (result enrich.Result, err error) {
	engine, closeRuntime, err := r.openForEvent(ctx, input.Event)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, closeRuntime()) }()
	return engine.Enrich(ctx, input)
}

// BuildContent 与 Event 丰富使用同一冻结来源版本；不能用新模式生成历史积压的内容。
func (r *releaseEnricher) BuildContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (content string, err error) {
	engine, closeRuntime, err := r.openForEvent(ctx, event)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, closeRuntime()) }()
	builder, ok := engine.(lifecycle.AlertContentBuilder)
	if !ok {
		return "", &description.Error{Code: "content_route_missing"}
	}
	return builder.BuildContent(ctx, event, evaluation, opening)
}

func (r *releaseEnricher) openForEvent(ctx context.Context, event domain.Event) (lifecycle.EventEnricher, func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if event.EventSourceID != r.current.EventSourceID || event.EventSourceVersion <= 0 {
		return nil, nil, fmt.Errorf("event enrich source scope mismatch")
	}
	if event.EventSourceVersion == r.current.Version {
		if r.current.RelatedTenantID != "" && r.current.RelatedTenantID != event.BKTenantID {
			return nil, nil, fmt.Errorf("event enrich tenant mismatch")
		}
		return r.engine, func() error { return nil }, nil
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	source, err := r.read(ctx, event.EventSourceID, event.EventSourceVersion)
	if err != nil {
		<-r.slots
		return nil, nil, err
	}
	if source.Version != event.EventSourceVersion || source.EventSourceID != event.EventSourceID || (source.RelatedTenantID != "" && source.RelatedTenantID != event.BKTenantID) {
		<-r.slots
		return nil, nil, fmt.Errorf("event enrich release scope mismatch")
	}
	engine, closeRuntime, err := r.open(ctx, source)
	if err != nil {
		<-r.slots
		return nil, nil, err
	}
	return engine, func() error { defer func() { <-r.slots }(); return closeRuntime() }, nil
}
