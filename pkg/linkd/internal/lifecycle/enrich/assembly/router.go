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
	"fmt"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/processors"
	"linkd/internal/lifecycle/enrich/rules"
)

// Router 依据 Alert.EventSourceID 选择启动时冻结的 Processor Chain。
type Router struct {
	routes map[string]lifecycle.AlertEnricher
}

// NewRouter 装配全部 enabled 和 disabled EventSource 的丰富路由。
func NewRouter(sources []config.EventSource, dataSources enrich.Sources) (*Router, error) {
	routes := make(map[string]lifecycle.AlertEnricher, len(sources))
	for sourceIndex, source := range sources {
		if _, exists := routes[source.EventSourceID]; exists {
			return nil, fmt.Errorf("event_sources[%d] duplicates event source %q", sourceIndex, source.EventSourceID)
		}
		chainProcessors := make([]enrich.Processor, 0, len(source.Enrich.Processors))
		for processorIndex, processorConfig := range source.Enrich.Processors {
			processor, err := newProcessor(processorConfig.Type)
			if err != nil {
				return nil, fmt.Errorf("event_sources[%d].enrich.processors[%d]: %w", sourceIndex, processorIndex, err)
			}
			chainProcessors = append(chainProcessors, processor)
		}
		if len(chainProcessors) == 0 {
			routes[source.EventSourceID] = enrich.NoopEnricher{}
			continue
		}
		chain, err := enrich.NewChain(chainProcessors, dataSources)
		if err != nil {
			return nil, fmt.Errorf("event_sources[%d].enrich: %w", sourceIndex, err)
		}
		routes[source.EventSourceID] = chain
	}
	return &Router{routes: routes}, nil
}

// Enrich 路由已知来源；未知来源作为装配与持久化不一致返回错误。
func (r *Router) Enrich(ctx context.Context, input lifecycle.EnrichInput) (lifecycle.EnrichResult, error) {
	if err := ctx.Err(); err != nil {
		return lifecycle.EnrichResult{}, err
	}
	enricher, exists := r.routes[input.Alert.EventSourceID]
	if !exists {
		return lifecycle.EnrichResult{}, fmt.Errorf("event source %q has no enrich route", input.Alert.EventSourceID)
	}
	return enricher.Enrich(ctx, input)
}

func newProcessor(name string) (enrich.Processor, error) {
	switch name {
	case rules.StrategyProcessor:
		return processors.Strategy{}, nil
	case rules.ResourceProcessor:
		return processors.Resource{}, nil
	case rules.DisplayProcessor:
		return processors.Display{}, nil
	case rules.MetricProcessor:
		return processors.Metric{}, nil
	case rules.SourceProcessor:
		return processors.EventSource{}, nil
	default:
		return nil, fmt.Errorf("processor type is not registered: %q", name)
	}
}
