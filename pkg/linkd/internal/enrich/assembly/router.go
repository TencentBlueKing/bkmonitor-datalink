// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/processors"
	"linkd/internal/enrich/rules"
)

// Router 依据 Event.EventSourceID 选择启动时冻结的 Processor Chain。
type Router struct {
	routes        map[string]executor
	chainKinds    map[string]enrich.ChainKind
	contentRoutes map[string]contentRoute
	digests       map[string]string
}

type contentRoute struct {
	version int64
	tenant  string
	builder *description.Builder
}

// NewRouter 装配全部 enabled 和 disabled EventSource 的丰富路由。
func NewRouter(sources []config.EventSource, dataSources enrich.Sources, options ...RouterOption) (*Router, error) {
	settings := routerOptions{observer: enrich.NoopObserver()}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	routes := make(map[string]executor, len(sources))
	chainKinds := make(map[string]enrich.ChainKind, len(sources))
	contentRoutes := make(map[string]contentRoute, len(sources))
	digests := make(map[string]string, len(sources))
	for sourceIndex, source := range sources {
		if _, exists := routes[source.EventSourceID]; exists {
			return nil, fmt.Errorf("event_sources[%d] duplicates event source %q", sourceIndex, source.EventSourceID)
		}
		if err := source.Enrich.Validate(); err != nil {
			return nil, err
		}
		route := contentRoute{version: source.Version, tenant: source.RelatedTenantID}
		if source.Enrich.EffectiveContentMode() == config.ContentModeBKMonitorDescription {
			if source.Version <= 0 {
				return nil, fmt.Errorf("bkmonitor_description requires a published source version")
			}
			builder, err := description.NewBuilder(settings.facts)
			if err != nil {
				return nil, fmt.Errorf("event_sources[%d].enrich.content_mode: %w", sourceIndex, err)
			}
			route.builder = builder
		}
		contentRoutes[source.EventSourceID] = route
		encoded, err := json.Marshal(source.Enrich)
		if err != nil {
			return nil, fmt.Errorf("encode enrich config: %w", err)
		}
		digest := sha256.Sum256(encoded)
		digests[source.EventSourceID] = hex.EncodeToString(digest[:])
		chainProcessors := make([]enrich.Processor, 0, len(source.Enrich.Processors))
		for processorIndex, processorConfig := range source.Enrich.Processors {
			processor, err := newProcessor(processorConfig)
			if err != nil {
				return nil, fmt.Errorf("event_sources[%d].enrich.processors[%d]: %w", sourceIndex, processorIndex, err)
			}
			if processorConfig.Type == rules.DisplayProcessor && route.builder != nil {
				processor = processors.Display{PreserveContent: true}
			}
			chainProcessors = append(chainProcessors, processor)
		}
		if len(chainProcessors) == 0 {
			routes[source.EventSourceID] = enrich.NoopEnricher{}
			chainKinds[source.EventSourceID] = enrich.ChainNoop
			continue
		}
		chain, err := enrich.NewChain(chainProcessors, dataSources, enrich.WithObserver(settings.observer))
		if err != nil {
			return nil, fmt.Errorf("event_sources[%d].enrich: %w", sourceIndex, err)
		}
		routes[source.EventSourceID] = chain
		chainKinds[source.EventSourceID] = enrich.ChainConfigured
	}
	return &Router{routes: routes, chainKinds: chainKinds, contentRoutes: contentRoutes, digests: digests}, nil
}

// BuildContent 依据冻结来源配置生成新 Alert 的内容；已保存 Alert 不调用此入口。
// 来源版本不符时必须保留事件等待正确配置，不能用当前模式处理旧 backlog。
func (r *Router) BuildContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if r == nil {
		return "", &description.Error{Code: "content_route_missing"}
	}
	route, ok := r.contentRoutes[event.EventSourceID]
	if !ok {
		return "", &description.Error{Code: "content_route_missing"}
	}
	if event.BKTenantID != opening.BKTenantID || event.EventSourceID != opening.EventSourceID ||
		(route.tenant != "" && route.tenant != event.BKTenantID) {
		return "", &description.Error{Code: "content_route_identity"}
	}
	if route.version > 0 && event.EventSourceVersion != route.version {
		return "", &description.Error{Code: "content_source_version"}
	}
	if route.builder == nil {
		return event.Content, nil
	}
	return route.builder.BuildContent(ctx, event, evaluation, opening)
}

// EnrichChainKind 返回指定 EventSource 使用的丰富链类型。
func (r *Router) EnrichChainKind(eventSourceID string) enrich.ChainKind {
	if r == nil {
		return enrich.ChainUnknown
	}
	kind, exists := r.chainKinds[eventSourceID]
	if !exists {
		return enrich.ChainUnknown
	}
	return kind
}

// Enrich 路由已知来源；未知来源作为装配与持久化不一致返回错误。
func (r *Router) Enrich(ctx context.Context, input enrich.Input) (enrich.Result, error) {
	if err := ctx.Err(); err != nil {
		return enrich.Result{}, err
	}
	enricher, exists := r.routes[input.Event.EventSourceID]
	if !exists {
		return enrich.Result{}, fmt.Errorf("event source %q has no enrich route", input.Event.EventSourceID)
	}
	result, err := enricher.Enrich(ctx, input)
	result.ConfigDigest = r.digests[input.Event.EventSourceID]
	result.ChainKind = r.chainKinds[input.Event.EventSourceID]
	return result, err
}

func newProcessor(config config.EnrichProcessorConfig) (enrich.Processor, error) {
	switch config.Type {
	case "cmdb", "fields":
		return processors.NewRules(config.Type, config.Config)
	case rules.TestProcessor:
		return processors.NewTest(config.Config)
	case rules.StrategyProcessor:
		return processors.NewStrategy(config.Config)
	case rules.ResourceProcessor:
		return processors.Resource{}, nil
	case rules.DisplayProcessor:
		return processors.Display{}, nil
	case rules.MetricProcessor:
		return processors.Metric{}, nil
	case rules.LogProcessor:
		return processors.Log{}, nil
	case "cloud_resource":
		return processors.CloudResourceProcessor{}, nil
	case "k8s":
		return processors.K8s{}, nil
	case rules.APMProcessor:
		return processors.APM{}, nil
	case rules.SourceProcessor:
		return processors.EventSource{}, nil
	default:
		return nil, fmt.Errorf("processor type is not registered: %q", config.Type)
	}
}

// executor 是路由器消费的执行端口；两种入口共享 Enrich 输入和结果。
type executor interface {
	Enrich(context.Context, enrich.Input) (enrich.Result, error)
}
